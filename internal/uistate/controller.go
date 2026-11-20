// Package uistate owns the desktop session state machine and all device
// access. It has no GTK dependency: results are marshalled to the UI thread by
// the caller through Options.Post.
package uistate

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/apperr"
	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/internal/usbdev"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/session"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

var errNoDevice = errors.New("uistate: no device connection")

// Options configures a Controller.
type Options struct {
	// Post runs fn on the UI thread. The GTK entry point supplies
	// glib.IdleAdd; tests can run it inline.
	Post func(fn func())

	Version   string
	Commit    string
	BuildDate string

	Timeout          time.Duration
	DiscoveryTimeout time.Duration
}

// Controller serializes device access and publishes immutable snapshots.
type Controller struct {
	opts Options

	mu       sync.Mutex
	snap     Snapshot
	listener func(Snapshot)
	closed   bool
	started  bool
	epoch    uint64
	listSeq  uint64
	watch    context.CancelFunc

	// opMu guards device and is held for every device call, so serial I/O
	// never interleaves between connect, watch, and user actions.
	opMu   sync.Mutex
	device session.Device
}

// New creates a controller in the disconnected state.
func New(opts Options) *Controller {
	if opts.Post == nil {
		opts.Post = func(fn func()) { fn() }
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 6 * time.Second
	}
	if opts.DiscoveryTimeout <= 0 {
		opts.DiscoveryTimeout = 15 * time.Second
	}
	return &Controller{opts: opts, snap: Snapshot{State: Disconnected}}
}

// SetListener installs the snapshot receiver. It is normally called once
// before Start, on the UI thread.
func (c *Controller) SetListener(listener func(Snapshot)) {
	c.mu.Lock()
	c.listener = listener
	snapshot := c.snap.clone()
	c.mu.Unlock()
	if listener != nil {
		c.opts.Post(func() { listener(snapshot) })
	}
}

// Start performs a non-invasive USB scan. It does not open or reset the port.
func (c *Controller) Start() {
	c.mu.Lock()
	if c.started || c.closed {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	c.Scan()
}

// Scan refreshes the list of candidate USB serial ports through sysfs.
func (c *Controller) Scan() {
	ports, err := transport.Candidates()
	c.update(func(s *Snapshot) {
		s.Ports = ports
		switch s.State {
		case Disconnected, USBDetected, Error:
			if len(ports) > 0 {
				if !contains(ports, s.Port) {
					s.Port = ports[0]
				}
				s.State = USBDetected
				s.USB = usbdev.Describe(s.Port)
			} else {
				s.State = Disconnected
				s.Port = ""
				s.USB = usbdev.Info{}
			}
		default:
			if s.Port != "" {
				s.USB = usbdev.Describe(s.Port)
			}
		}
		if err != nil {
			info := apperr.Classify(err)
			s.Error = &info
		}
	})
}

// Connect opens the selected port and reads identity and status. It never
// retries automatically.
func (c *Controller) Connect(port string) {
	c.mu.Lock()
	if c.closed || c.snap.State == Connecting || c.snap.State.DeviceOpen() {
		c.mu.Unlock()
		return
	}
	if port == "" {
		port = c.snap.Port
	}
	if port == "" && len(c.snap.Ports) > 0 {
		port = c.snap.Ports[0]
	}
	if port == "" {
		c.mu.Unlock()
		c.setLocalError("no VaultKey port is available")
		return
	}
	c.epoch++
	epoch := c.epoch
	c.mu.Unlock()

	c.stopWatch()
	c.update(func(s *Snapshot) {
		s.State = Connecting
		s.Port = port
		s.Error = nil
		s.USB = usbdev.Describe(port)
		s.Hello = vaultkey.Hello{}
		s.HasHello = false
		s.Status = vaultkey.Status{}
		s.HasStatus = false
		s.Clock = vaultkey.Clock{}
		s.HasClock = false
		s.ClockErr = ""
		s.Credentials = nil
		s.TOTP = nil
		s.CredentialTotal = 0
		s.TOTPTotal = 0
	})

	go c.open(epoch, port)
}

func (c *Controller) open(epoch uint64, port string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.DiscoveryTimeout)
	defer cancel()
	device, err := vaultkey.Open(ctx, vaultkey.OpenOptions{
		Port:         port,
		ProbeTimeout: 3 * time.Second,
		Version:      c.opts.Version,
		Commit:       c.opts.Commit,
		BuildDate:    c.opts.BuildDate,
	})
	if err != nil {
		c.connectFailed(epoch, port, err)
		return
	}

	c.opMu.Lock()
	if !c.currentLocked(epoch) {
		c.opMu.Unlock()
		_ = device.Close()
		return
	}
	c.device = device
	c.opMu.Unlock()

	hello := device.Hello()
	c.updateIf(epoch, func(s *Snapshot) {
		if s.State != Connecting {
			return
		}
		s.Hello = hello
		s.HasHello = true
		s.State = Connected
	})

	status, statusErr := c.readStatus()
	if statusErr != nil && isConnectionLoss(statusErr) {
		c.disconnectWithError(epoch, statusErr)
		return
	}
	c.updateIf(epoch, func(s *Snapshot) {
		if s.State != Connecting && s.State != Connected {
			return
		}
		if statusErr == nil {
			s.Status = status
			s.HasStatus = true
			s.StatusAt = time.Now()
			s.State = deriveState(status, nil)
		} else {
			info := apperr.Classify(statusErr)
			s.Error = &info
		}
	})

	c.readClock(epoch)
	c.startWatch(epoch)
}

func (c *Controller) connectFailed(epoch uint64, port string, err error) {
	info := apperr.Classify(err)
	c.updateIf(epoch, func(s *Snapshot) {
		s.State = Error
		s.Port = port
		s.USB = usbdev.Describe(port)
		s.Error = &info
	})
}

// Disconnect closes the device explicitly. Closing the port drops DTR and
// revokes the firmware's temporary CDC authorization.
func (c *Controller) Disconnect() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.epoch++
	c.mu.Unlock()

	c.stopWatch()
	c.opMu.Lock()
	device := c.device
	c.device = nil
	c.opMu.Unlock()
	if device != nil {
		_ = device.Close()
	}

	c.update(func(s *Snapshot) {
		s.State = Disconnected
		s.Hello = vaultkey.Hello{}
		s.HasHello = false
		s.Status = vaultkey.Status{}
		s.HasStatus = false
		s.Clock = vaultkey.Clock{}
		s.HasClock = false
		s.ClockErr = ""
		s.Credentials = nil
		s.TOTP = nil
		s.CredentialTotal = 0
		s.TOTPTotal = 0
		s.Listing = false
		s.Error = nil
	})
	c.Scan()
}

// Refresh scans USB state and, when open, refreshes status and clock.
func (c *Controller) Refresh() {
	c.Scan()
	c.mu.Lock()
	epoch := c.epoch
	open := c.snap.State.DeviceOpen()
	c.mu.Unlock()
	if !open {
		return
	}
	go func() {
		status, err := c.readStatus()
		if err != nil {
			if isConnectionLoss(err) {
				c.disconnectWithError(epoch, err)
				return
			}
			c.update(func(s *Snapshot) {
				info := apperr.Classify(err)
				s.Error = &info
			})
			return
		}
		c.updateIf(epoch, func(s *Snapshot) {
			s.Status = status
			s.HasStatus = true
			s.StatusAt = time.Now()
			s.Error = nil
			s.State = deriveState(status, nil)
		})
		c.readClock(epoch)
	}()
}

// RefreshList loads every page of credential and TOTP labels matching query.
func (c *Controller) RefreshList(query string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.listSeq++
	seq := c.listSeq
	epoch := c.epoch
	open := c.snap.State.DeviceOpen()
	c.mu.Unlock()

	if !open {
		c.update(func(s *Snapshot) {
			s.Credentials = nil
			s.TOTP = nil
			s.CredentialTotal = 0
			s.TOTPTotal = 0
			s.ListRevision++
		})
		return
	}

	c.update(func(s *Snapshot) { s.Listing = true })
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := c.readList(ctx, query)

		c.mu.Lock()
		current := !c.closed && c.epoch == epoch && c.listSeq == seq
		c.mu.Unlock()
		if !current {
			return
		}

		c.update(func(s *Snapshot) {
			s.Listing = false
			if err != nil {
				info := apperr.Classify(err)
				s.Error = &info
				return
			}
			s.Credentials = result.Credentials
			s.TOTP = result.TOTP
			s.CredentialTotal = result.CredentialTotal
			s.TOTPTotal = result.TOTPTotal
			s.ListRevision++
		})
	}()
}

// Lock requests LOCK_DEVICE.
func (c *Controller) Lock() {
	c.mu.Lock()
	epoch := c.epoch
	c.mu.Unlock()
	go func() {
		err := c.useDevice(func(ctx context.Context, device session.Device) error {
			return device.Lock(ctx)
		})
		if err != nil {
			c.update(func(s *Snapshot) {
				info := apperr.Classify(err)
				s.Error = &info
			})
			return
		}
		c.updateIf(epoch, func(s *Snapshot) {
			s.State = Locked
			s.Status.Locked = true
		})
	}()
}

// AddCredential stores a complete credential. The firmware has no partial
// update, so every field is sent.
func (c *Controller) AddCredential(input vaultkey.CredentialInput, done func(error)) {
	go func() {
		err := c.useDevice(func(ctx context.Context, device session.Device) error {
			_, err := device.AddCredential(ctx, input)
			return err
		})
		c.finish(done, err)
		if err == nil {
			c.refreshAfterMutation()
		}
	}()
}

// AddTOTP stores a TOTP URI.
func (c *Controller) AddTOTP(uri string, done func(error)) {
	go func() {
		err := c.useDevice(func(ctx context.Context, device session.Device) error {
			_, err := device.AddTOTP(ctx, uri)
			return err
		})
		c.finish(done, err)
		if err == nil {
			c.refreshAfterMutation()
		}
	}()
}

// Delete removes one credential or TOTP entry.
func (c *Controller) Delete(entryType vaultkey.EntryType, id uint32, done func(error)) {
	go func() {
		err := c.useDevice(func(ctx context.Context, device session.Device) error {
			return device.Delete(ctx, entryType, id)
		})
		c.finish(done, err)
		if err == nil {
			c.refreshAfterMutation()
		}
	}()
}

// SynchronizeClock writes the host UTC time to the device and verifies it.
func (c *Controller) SynchronizeClock(done func(error)) {
	now := time.Now().UTC()
	go func() {
		var clock vaultkey.Clock
		err := c.useDevice(func(ctx context.Context, device session.Device) error {
			var err error
			clock, err = device.SetTime(ctx, now.Unix())
			return err
		})
		if err == nil {
			c.update(func(s *Snapshot) {
				clock.UTC = true
				s.Clock = clock
				s.HasClock = true
				s.ClockErr = ""
				s.Error = nil
			})
		}
		c.finish(done, err)
	}()
}

// Close releases the device and stops all background work.
func (c *Controller) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.epoch++
	c.mu.Unlock()

	c.stopWatch()
	c.opMu.Lock()
	device := c.device
	c.device = nil
	c.opMu.Unlock()
	if device != nil {
		_ = device.Close()
	}
}

func (c *Controller) refreshAfterMutation() {
	c.Refresh()
	c.RefreshList("")
}

func (c *Controller) startWatch(epoch uint64) {
	c.stopWatch()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.closed || c.epoch != epoch {
		c.mu.Unlock()
		cancel()
		return
	}
	c.watch = cancel
	c.mu.Unlock()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !c.watchStatus(epoch) {
					return
				}
			}
		}
	}()
}

func (c *Controller) stopWatch() {
	c.mu.Lock()
	cancel := c.watch
	c.watch = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Controller) watchStatus(epoch uint64) bool {
	status, err := c.readStatus()
	if err != nil {
		if isConnectionLoss(err) {
			c.disconnectWithError(epoch, err)
			return false
		}
		// Read errors on an open port are not fatal by themselves; the
		// next tick retries and the explicit Refresh reports the error.
		return true
	}
	c.updateIf(epoch, func(s *Snapshot) {
		s.Status = status
		s.HasStatus = true
		s.StatusAt = time.Now()
		s.State = deriveState(status, nil)
	})
	return true
}

func (c *Controller) disconnectWithError(epoch uint64, cause error) {
	c.mu.Lock()
	if c.closed || c.epoch != epoch {
		c.mu.Unlock()
		return
	}
	c.epoch++
	c.mu.Unlock()

	c.stopWatch()
	c.opMu.Lock()
	device := c.device
	c.device = nil
	c.opMu.Unlock()
	if device != nil {
		_ = device.Close()
	}

	info := apperr.Classify(cause)
	c.update(func(s *Snapshot) {
		s.State = Disconnected
		s.Hello = vaultkey.Hello{}
		s.HasHello = false
		s.Status = vaultkey.Status{}
		s.HasStatus = false
		s.Clock = vaultkey.Clock{}
		s.HasClock = false
		s.ClockErr = ""
		s.Credentials = nil
		s.TOTP = nil
		s.CredentialTotal = 0
		s.TOTPTotal = 0
		s.Listing = false
		s.Error = &info
	})
	c.Scan()
}

func (c *Controller) readStatus() (vaultkey.Status, error) {
	var status vaultkey.Status
	err := c.useDevice(func(ctx context.Context, device session.Device) error {
		var err error
		status, err = device.Status(ctx)
		return err
	})
	return status, err
}

func (c *Controller) readClock(epoch uint64) {
	var clock vaultkey.Clock
	err := c.useDevice(func(ctx context.Context, device session.Device) error {
		var err error
		clock, err = device.Time(ctx)
		return err
	})
	c.updateIf(epoch, func(s *Snapshot) {
		if err != nil {
			s.HasClock = false
			s.ClockErr = apperr.Classify(err).Message
			return
		}
		s.Clock = clock
		s.HasClock = true
		s.ClockErr = ""
	})
}

func (c *Controller) readList(ctx context.Context, query string) (vaultkey.ServiceList, error) {
	var result vaultkey.ServiceList
	err := c.useDevice(func(inner context.Context, device session.Device) error {
		var err error
		result, err = device.ListAll(inner, query, c.opts.Timeout)
		return err
	})
	return result, err
}

func (c *Controller) useDevice(fn func(context.Context, session.Device) error) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.device == nil {
		return errNoDevice
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.Timeout)
	defer cancel()
	return fn(ctx, c.device)
}

func (c *Controller) finish(done func(error), err error) {
	if done == nil {
		return
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	c.opts.Post(func() { done(err) })
}

func (c *Controller) setLocalError(message string) {
	info := vaultkey.ErrorInfo{
		Kind: "connection", Code: "no_port", Message: message,
		Hint:     "connect the VaultKey with a USB data cable and refresh.",
		ExitCode: vaultkey.ExitConnection,
	}
	c.update(func(s *Snapshot) { s.Error = &info })
}

func (c *Controller) update(fn func(*Snapshot)) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	fn(&c.snap)
	snapshot := c.snap.clone()
	listener := c.listener
	c.mu.Unlock()
	if listener != nil {
		c.opts.Post(func() { listener(snapshot) })
	}
}

// updateIf publishes only when epoch is still the active session.
func (c *Controller) updateIf(epoch uint64, fn func(*Snapshot)) {
	c.mu.Lock()
	if c.closed || c.epoch != epoch {
		c.mu.Unlock()
		return
	}
	fn(&c.snap)
	snapshot := c.snap.clone()
	listener := c.listener
	c.mu.Unlock()
	if listener != nil {
		c.opts.Post(func() { listener(snapshot) })
	}
}

// current reports whether epoch is still the active session.
func (c *Controller) current(epoch uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.epoch == epoch
}

// currentLocked is current for callers that do not hold mu.
func (c *Controller) currentLocked(epoch uint64) bool { return c.current(epoch) }

func deriveState(status vaultkey.Status, err error) State {
	if err != nil {
		return Connected
	}
	if status.Locked {
		return Locked
	}
	if status.Provisioning {
		return Authenticated
	}
	return Connected
}

func isConnectionLoss(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, vaultkey.ErrClosed) {
		return true
	}
	info := apperr.Classify(err)
	return info.Kind == "connection" && info.Code != "port_busy"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
