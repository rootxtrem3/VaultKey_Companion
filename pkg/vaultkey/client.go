package vaultkey

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
)

const maxResponseLine = 64 * 1024

type frame struct {
	id     string
	ok     bool
	code   string
	detail string
	raw    []byte
}

// Client implements one serialized request at a time over VaultKey CDC JSON
// Lines. It never retries a mutating request automatically.
type Client struct {
	port      transport.Port
	frames    chan frame
	done      chan struct{}
	closeOnce sync.Once
	requestMu sync.Mutex

	errMu    sync.Mutex
	readErr  error
	closeErr error

	logMu sync.Mutex
	log   func(string)

	requestSequence uint64
}

// New starts the response reader for an open port.
func New(port transport.Port) *Client {
	client := &Client{
		port:   port,
		frames: make(chan frame, 4),
		done:   make(chan struct{}),
	}
	go client.readLoop()
	return client
}

// SetLogger installs a diagnostic logger. It receives command names, sizes,
// and status only; request fields and response bodies are never logged.
func (c *Client) SetLogger(logger func(string)) {
	c.logMu.Lock()
	c.log = logger
	c.logMu.Unlock()
}

// PortName returns the underlying serial port path.
func (c *Client) PortName() string { return c.port.Name() }

// Done is closed when the connection is no longer usable.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err reports the terminal connection error, or nil while the port is open.
// An intentional Close reports ErrClosed.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.readErr
}

func (c *Client) logf(format string, arguments ...any) {
	c.logMu.Lock()
	logger := c.log
	c.logMu.Unlock()
	if logger != nil {
		logger(fmt.Sprintf(format, arguments...))
	}
}

// Close closes the underlying serial port.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		// Record the intentional close before shutting down the port so the
		// reader's inevitable I/O error cannot mask it. Later calls then report
		// ErrClosed deterministically instead of a platform-specific closed-pipe
		// error.
		c.errMu.Lock()
		if c.readErr == nil {
			c.readErr = ErrClosed
		}
		c.errMu.Unlock()
		close(c.done)
		c.closeErr = c.port.Close()
	})
	return c.closeErr
}

func (c *Client) fail(err error) {
	c.errMu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.errMu.Unlock()
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.port.Close()
	})
}

func (c *Client) lastReadError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.readErr
}

func (c *Client) readLoop() {
	reader := bufio.NewReaderSize(&idleReader{reader: c.port, done: c.done}, maxResponseLine)
	for {
		line, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			c.fail(&ProtocolError{Message: "response line exceeds 64 KiB"})
			return
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.fail(ErrClosed)
			} else {
				c.fail(err)
			}
			return
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		parsed, err := parseFrame(line)
		if err != nil {
			c.fail(err)
			return
		}
		select {
		case c.frames <- parsed:
		case <-c.done:
			return
		}
	}
}

func parseFrame(line []byte) (frame, error) {
	var envelope struct {
		Version *int    `json:"v"`
		ID      *string `json:"id"`
		OK      *bool   `json:"ok"`
		Error   *string `json:"error"`
		Detail  *string `json:"detail"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return frame{}, &ProtocolError{Message: "response is not a JSON object"}
	}
	if envelope.Version == nil || *envelope.Version != ProtocolVersion {
		return frame{}, &ProtocolError{Message: "unsupported response protocol version"}
	}
	if envelope.ID == nil || *envelope.ID == "" {
		return frame{}, &ProtocolError{Message: "response has no request id"}
	}
	if envelope.OK == nil {
		return frame{}, &ProtocolError{Message: "response has no ok field"}
	}
	result := frame{
		id:  *envelope.ID,
		ok:  *envelope.OK,
		raw: append([]byte(nil), line...),
	}
	if !result.ok {
		if envelope.Error == nil || *envelope.Error == "" {
			return frame{}, &ProtocolError{Message: "error response has no error code"}
		}
		result.code = *envelope.Error
		if envelope.Detail != nil {
			result.detail = *envelope.Detail
		}
	}
	return result, nil
}

// Do sends one command. mutating must be true for any command whose effect
// cannot be ruled out when the response is lost (add, update, delete, time,
// lock, factory actions). A lost mutating response returns *AmbiguousError.
func (c *Client) Do(ctx context.Context, command string, fields map[string]any,
	mutating bool) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if command == "" {
		return nil, &RequestError{Message: "command is required"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.requestMu.Lock()
	defer c.requestMu.Unlock()

	select {
	case <-c.done:
		if err := c.lastReadError(); err != nil && !errors.Is(err, ErrClosed) {
			return nil, err
		}
		return nil, ErrClosed
	default:
	}

	sequence := time.Now().UnixNano()
	c.requestSequence++
	id := fmt.Sprintf("vk-%d-%d", sequence, c.requestSequence)
	payload := make(map[string]any, len(fields)+3)
	payload["v"] = ProtocolVersion
	payload["id"] = id
	payload["cmd"] = command
	for key, value := range fields {
		switch key {
		case "v", "id", "cmd":
			return nil, &RequestError{Message: "reserved request field: " + key}
		}
		payload[key] = value
	}

	encoded, err := marshalRequest(payload)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxLineLength {
		return nil, &RequestError{Message: fmt.Sprintf(
			"request is %d bytes; the device limit is %d bytes", len(encoded), MaxLineLength)}
	}

	c.logf("-> %s (%d bytes)", command, len(encoded)+1)
	wire := append(encoded, '\n')
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- writeAll(c.port, wire)
	}()

	select {
	case err := <-writeResult:
		if err != nil {
			c.fail(err)
			return nil, c.abort(command, mutating, err)
		}
	case <-c.done:
		err := c.lastReadError()
		if err == nil {
			err = ErrClosed
		}
		return nil, c.abort(command, mutating, err)
	case <-ctx.Done():
		c.fail(ctx.Err())
		return nil, c.abort(command, mutating, ctx.Err())
	}

	for {
		select {
		case <-c.done:
			err := c.lastReadError()
			if err == nil {
				err = ErrClosed
			}
			if _, protocol := err.(*ProtocolError); protocol {
				return nil, err
			}
			return nil, c.abort(command, mutating, err)
		case <-ctx.Done():
			c.fail(ctx.Err())
			return nil, c.abort(command, mutating, ctx.Err())
		case received := <-c.frames:
			if received.id != id {
				c.fail(&ProtocolError{Message: "response id does not match request"})
				return nil, &ProtocolError{Message: "response id does not match request"}
			}
			c.logf("<- %s ok=%t (%d bytes)", command, received.ok, len(received.raw))
			if !received.ok {
				return nil, &DeviceError{Code: received.code, Detail: received.detail}
			}
			return json.RawMessage(received.raw), nil
		}
	}
}

func (c *Client) abort(command string, mutating bool, cause error) error {
	if mutating {
		return &AmbiguousError{Command: command, Cause: cause}
	}
	return cause
}

func marshalRequest(payload map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, &RequestError{Message: "could not encode request: " + err.Error()}
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
