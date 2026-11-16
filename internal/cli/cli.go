package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/prompt"
	"github.com/rootxtrem3/VaultKey_Companion/internal/transport"
	"github.com/rootxtrem3/VaultKey_Companion/internal/vaultkey"
)

const (
	exitOK                 = 0
	exitFailure            = 1
	exitUsage              = 2
	exitConnection         = 3
	exitLocked             = 4
	exitNotAuthorized      = 5
	exitProtocol           = 6
	exitAmbiguousOrTimeout = 7
	exitDeviceOperation    = 8
	exitInterrupted        = 130
)

const defaultTimeout = 5 * time.Second

type app struct {
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	version   string
	commit    string
	buildDate string

	port    string
	timeout time.Duration
	debug   bool
	ctx     context.Context
}

type globalOptions struct {
	port    string
	timeout time.Duration
	debug   bool
	version bool
}

func parseGlobals(arguments []string) (globalOptions, []string, error) {
	options := globalOptions{
		port:    os.Getenv("VAULTKEY_PORT"),
		timeout: defaultTimeout,
	}
	if raw := os.Getenv("VAULTKEY_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return options, nil, fmt.Errorf("VAULTKEY_TIMEOUT: %w", err)
		}
		options.timeout = parsed
	}

	remaining := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			remaining = append(remaining, arguments[index+1:]...)
			break
		}
		switch {
		case argument == "--port" || argument == "-port":
			if index+1 >= len(arguments) {
				return options, nil, errors.New("--port requires a value")
			}
			index++
			options.port = arguments[index]
		case strings.HasPrefix(argument, "--port="):
			options.port = strings.TrimPrefix(argument, "--port=")
		case strings.HasPrefix(argument, "-port="):
			options.port = strings.TrimPrefix(argument, "-port=")
		case argument == "--timeout" || argument == "-timeout":
			if index+1 >= len(arguments) {
				return options, nil, errors.New("--timeout requires a duration")
			}
			index++
			parsed, err := time.ParseDuration(arguments[index])
			if err != nil {
				return options, nil, fmt.Errorf("--timeout: %w", err)
			}
			options.timeout = parsed
		case strings.HasPrefix(argument, "--timeout="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(argument, "--timeout="))
			if err != nil {
				return options, nil, fmt.Errorf("--timeout: %w", err)
			}
			options.timeout = parsed
		case strings.HasPrefix(argument, "-timeout="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(argument, "-timeout="))
			if err != nil {
				return options, nil, fmt.Errorf("--timeout: %w", err)
			}
			options.timeout = parsed
		case argument == "--debug" || argument == "-debug":
			options.debug = true
		case argument == "--version" || argument == "-version":
			options.version = true
		default:
			remaining = append(remaining, argument)
		}
	}

	if options.timeout < 500*time.Millisecond || options.timeout > time.Minute {
		return options, nil, errors.New("--timeout must be between 500ms and 1m")
	}
	return options, remaining, nil
}

// Run executes the companion CLI and returns a process exit code.
func Run(arguments []string, stdin io.Reader, stdout, stderr io.Writer,
	version, commit, buildDate string) int {
	options, remaining, err := parseGlobals(arguments)
	if err != nil {
		fmt.Fprintf(stderr, "vaultkey: %v\n", err)
		fmt.Fprintln(stderr, `Try "vaultkey help" for usage.`)
		return exitUsage
	}
	if options.version {
		printVersion(stdout, version, commit, buildDate)
		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	application := &app{
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		version:   version,
		commit:    commit,
		buildDate: buildDate,
		port:      options.port,
		timeout:   options.timeout,
		debug:     options.debug,
		ctx:       ctx,
	}

	if len(remaining) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	command := remaining[0]
	arguments = remaining[1:]
	switch command {
	case "help", "-h", "--help":
		if len(arguments) > 0 {
			printCommandHelp(stdout, arguments[0])
		} else {
			printUsage(stdout)
		}
		return exitOK
	case "version":
		printVersion(stdout, version, commit, buildDate)
		return exitOK
	case "devices":
		return application.runDevices(arguments)
	case "info":
		return application.runInfo(arguments)
	case "status":
		return application.runStatus(arguments)
	case "doctor":
		return application.runDoctor(arguments)
	case "time":
		return application.runTime(arguments)
	case "list":
		return application.runList(arguments)
	case "add-credential":
		return application.runAddCredential(arguments)
	case "update-credential":
		return application.runUpdateCredential(arguments)
	case "add-totp":
		return application.runAddTOTP(arguments)
	case "delete":
		return application.runDelete(arguments)
	case "lock":
		return application.runLock(arguments)
	case "watch":
		return application.runWatch(arguments)
	default:
		fmt.Fprintf(stderr, "vaultkey: unknown command %q\n", command)
		fmt.Fprintln(stderr, `Try "vaultkey help" for usage.`)
		return exitUsage
	}
}

func (a *app) timeoutContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.ctx, a.timeout)
}

func (a *app) debugf(format string, arguments ...any) {
	if a.debug {
		fmt.Fprintf(a.stderr, "debug: "+format+"\n", arguments...)
	}
}

func newFlagSet(name, summary string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(stderr, "%s\n\nUsage: vaultkey %s [options]\n", summary, name)
	}
	return flags
}

func parseFlags(flags *flag.FlagSet, arguments []string) (bool, int) {
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, exitOK
		}
		return false, exitUsage
	}
	return true, exitOK
}

func (a *app) fail(err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(a.stderr, "interrupted")
		return exitInterrupted
	}

	var ambiguous *vaultkey.AmbiguousError
	if errors.As(err, &ambiguous) {
		fmt.Fprintf(a.stderr, "error: %v\n", ambiguous)
		fmt.Fprintln(a.stderr, "warning: the command may have been applied. Reconnect and verify before retrying.")
		return exitAmbiguousOrTimeout
	}

	var request *vaultkey.RequestError
	if errors.As(err, &request) {
		fmt.Fprintf(a.stderr, "error: %v\n", request)
		return exitUsage
	}

	var protocol *vaultkey.ProtocolError
	if errors.As(err, &protocol) {
		fmt.Fprintf(a.stderr, "error: %v\n", protocol)
		fmt.Fprintln(a.stderr, "hint: reconnect the device. If this persists, update the app or firmware so both use protocol v1.")
		return exitProtocol
	}

	var device *vaultkey.DeviceError
	if errors.As(err, &device) {
		message, code := deviceMessage(device)
		fmt.Fprintf(a.stderr, "error: %s (%s)\n", message, device.Code)
		if hint := deviceHint(device); hint != "" {
			fmt.Fprintf(a.stderr, "hint: %s\n", hint)
		}
		return code
	}

	if errors.Is(err, prompt.ErrNoTerminal) {
		fmt.Fprintln(a.stderr, "error: no interactive terminal is available")
		fmt.Fprintln(a.stderr, "hint: run from a terminal, or use the command's non-interactive option (--yes, --password-stdin, or --uri-stdin).")
		return exitUsage
	}
	if errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(a.stderr, "error: the device did not respond before the timeout")
		fmt.Fprintln(a.stderr, "hint: check the cable and port, or increase --timeout")
		return exitAmbiguousOrTimeout
	}
	if errors.Is(err, vaultkey.ErrClosed) {
		fmt.Fprintln(a.stderr, "error: the device connection was closed")
		fmt.Fprintln(a.stderr, "hint: reconnect the device and try again.")
		return exitConnection
	}
	if errors.Is(err, transport.ErrUnsupported) {
		fmt.Fprintln(a.stderr, "error: serial ports are not supported on this platform")
		return exitConnection
	}
	if os.IsPermission(err) {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		fmt.Fprintln(a.stderr, "hint: on Linux add your user to the dialout group, or adjust the device permissions.")
		return exitConnection
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		fmt.Fprintln(a.stderr, "hint: check that the device is connected and the port is correct.")
		return exitConnection
	}
	var notFound *notFoundError
	if errors.As(err, &notFound) {
		fmt.Fprintf(a.stderr, "error: %v\n", notFound)
		fmt.Fprintln(a.stderr, "hint: connect the VaultKey, make sure its firmware is running, and pass --port if needed.")
		return exitConnection
	}
	var multiple *multipleDevicesError
	if errors.As(err, &multiple) {
		fmt.Fprintf(a.stderr, "error: %v\n", multiple)
		fmt.Fprintln(a.stderr, "hint: choose one port with --port.")
		return exitConnection
	}
	if transport.IsNotSerial(err) {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		fmt.Fprintln(a.stderr, "hint: choose the VaultKey CDC port (for example /dev/ttyACM0 or COM5).")
		return exitConnection
	}

	fmt.Fprintf(a.stderr, "error: %v\n", err)
	return exitFailure
}

func deviceMessage(err *vaultkey.DeviceError) (string, int) {
	switch err.Code {
	case "device_locked":
		return "the device is locked", exitLocked
	case "provisioning_not_authorized":
		return "CDC provisioning is not authorized", exitNotAuthorized
	case "rtc_time_invalid":
		return "the RTC time is not valid", exitDeviceOperation
	case "storage_failed":
		return "the device could not save the change", exitDeviceOperation
	case "delete_failed":
		return "the device could not delete the entry", exitDeviceOperation
	case "entry_not_found":
		return "the entry was not found", exitDeviceOperation
	case "invalid_credential", "invalid_query", "invalid_pagination",
		"invalid_time", "invalid_uri", "invalid_totp", "invalid_entry_type":
		return "the device rejected the request: " + err.Code, exitUsage
	default:
		return "the device returned an error", exitFailure
	}
}

func deviceHint(err *vaultkey.DeviceError) string {
	switch err.Code {
	case "device_locked":
		return "unlock the device with its PIN, then retry."
	case "provisioning_not_authorized":
		return `on the device open USB Functions and choose "Authorize CDC 5 min".`
	case "rtc_time_invalid":
		return "set the clock with `vaultkey time set`, or from the device settings."
	case "invalid_totp":
		if err.Detail != "" {
			return "the TOTP URI was rejected: " + err.Detail
		}
		return "check the otpauth://totp/ URI, Base32 secret, digits, period, and algorithm."
	default:
		return ""
	}
}

func (a *app) connect() (*vaultkey.Client, vaultkey.Hello, error) {
	port := a.port
	if port == "" {
		detected, err := a.detectPort()
		if err != nil {
			return nil, vaultkey.Hello{}, err
		}
		port = detected
		a.debugf("auto-detected %s", port)
	}
	return a.openPort(port)
}

func (a *app) openPort(port string) (*vaultkey.Client, vaultkey.Hello, error) {
	serial, err := transport.Open(port)
	if err != nil {
		return nil, vaultkey.Hello{}, err
	}
	client := vaultkey.New(serial)
	if a.debug {
		client.SetLogger(func(message string) { fmt.Fprintln(a.stderr, "debug: "+message) })
	}

	ctx, cancel := context.WithTimeout(a.ctx, a.probeTimeout())
	defer cancel()
	hello, err := client.Hello(ctx)
	if err != nil {
		client.Close()
		return nil, vaultkey.Hello{}, err
	}
	if hello.Product != "VaultKey" {
		client.Close()
		return nil, vaultkey.Hello{}, fmt.Errorf(
			"%s responded as %q, not VaultKey", port, hello.Product)
	}
	if hello.Protocol != vaultkey.ProtocolVersion {
		client.Close()
		return nil, vaultkey.Hello{}, &vaultkey.ProtocolError{Message: fmt.Sprintf(
			"device protocol is %d; this app supports %d", hello.Protocol, vaultkey.ProtocolVersion)}
	}
	return client, hello, nil
}

func (a *app) probeTimeout() time.Duration {
	timeout := 2 * time.Second
	if a.timeout < timeout {
		timeout = a.timeout
	}
	return timeout
}

type probeResult struct {
	port    string
	hello   vaultkey.Hello
	err     error
	product string
}

type notFoundError struct {
	results []probeResult
	reason  error
}

func (e *notFoundError) Error() string {
	if e.reason != nil {
		return "could not find a VaultKey: " + e.reason.Error()
	}
	if len(e.results) == 0 {
		return "no candidate VaultKey serial ports were found"
	}
	var builder strings.Builder
	builder.WriteString("no VaultKey answered on the candidate ports:")
	for _, result := range e.results {
		builder.WriteString("\n  ")
		builder.WriteString(result.port)
		builder.WriteString(": ")
		if result.err != nil {
			builder.WriteString(result.err.Error())
		} else if result.product != "" {
			builder.WriteString("device identifies as " + result.product)
		} else {
			builder.WriteString("no valid response")
		}
	}
	return builder.String()
}

type multipleDevicesError struct {
	ports []string
}

func (e *multipleDevicesError) Error() string {
	return "multiple VaultKey devices found: " + strings.Join(e.ports, ", ")
}

func (a *app) detectPort() (string, error) {
	ports, err := transport.Candidates()
	if err != nil {
		return "", &notFoundError{reason: err}
	}
	if len(ports) == 0 {
		return "", &notFoundError{}
	}

	results := make([]probeResult, 0, len(ports))
	vaultkeys := make([]string, 0, 1)
	for _, port := range ports {
		result := a.probe(port)
		results = append(results, result)
		if result.err == nil && result.product == "VaultKey" {
			vaultkeys = append(vaultkeys, port)
		}
	}
	sort.Strings(vaultkeys)
	switch len(vaultkeys) {
	case 0:
		return "", &notFoundError{results: results}
	case 1:
		return vaultkeys[0], nil
	default:
		return "", &multipleDevicesError{ports: vaultkeys}
	}
}

func (a *app) probe(port string) probeResult {
	result := probeResult{port: port}
	serial, err := transport.Open(port)
	if err != nil {
		result.err = err
		return result
	}
	client := vaultkey.New(serial)
	defer client.Close()

	ctx, cancel := context.WithTimeout(a.ctx, a.probeTimeout())
	defer cancel()
	hello, err := client.Hello(ctx)
	if err != nil {
		result.err = err
		return result
	}
	result.hello = hello
	result.product = hello.Product
	return result
}

func printVersion(writer io.Writer, version, commit, buildDate string) {
	if commit == "" {
		commit = "unknown"
	}
	if buildDate == "" {
		buildDate = "unknown"
	}
	fmt.Fprintf(writer, "vaultkey %s (commit %s, built %s)\n", version, commit, buildDate)
}

func printUsage(writer io.Writer) {
	fmt.Fprint(writer, `VaultKey companion app

Usage:
  vaultkey [global options] <command> [command options]

Global options:
  --port PATH       Serial port (default: auto-detect, env VAULTKEY_PORT)
  --timeout DUR     Per-request timeout, 500ms-1m (default 5s)
  --debug           Log command names, sizes, and status only
  --version         Print the app version
  -h, --help        Show help

Commands:
  devices              Probe candidate serial ports for VaultKey
  info                 Show device identity
  status               Show lock, RTC, storage, and USB state
  doctor               Diagnose connection and authorization
  time get             Read device UTC time
  time set             Set device UTC time (default: now)
  list                 List credential and TOTP labels
  add-credential       Add a credential (password prompted)
  update-credential    Replace a credential by ID
  add-totp             Import an otpauth://totp/ URI
  delete               Delete an entry (confirmation required)
  lock                 Lock the vault and revoke CDC authorization
  watch                Poll status until interrupted
  version              Print the app version

Managing credentials requires:
  1. unlock the device with its PIN;
  2. keep the USB CDC port open; and
  3. on the device choose USB Functions -> "Authorize CDC 5 min".

The app never reads passwords back, never bypasses the PIN, and never logs
request fields. Mutating commands are not retried automatically.

Run "vaultkey help <command>" for command options.
`)
}

func printCommandHelp(writer io.Writer, command string) {
	switch command {
	case "devices":
		fmt.Fprint(writer, `Usage: vaultkey devices [--json]

Probes /dev/ttyACM*, /dev/ttyUSB* (Linux), /dev/cu.usbmodem* (macOS), or the
Windows SERIALCOMM registry. Each candidate is opened and sent HELLO. Use
--port to avoid probing other serial devices.
`)
	case "info":
		fmt.Fprint(writer, "Usage: vaultkey info [--json]\n\nShows VaultKey product, firmware, hardware, protocol, and port.\n")
	case "status":
		fmt.Fprint(writer, "Usage: vaultkey status [--json]\n\nShows lock, RTC, storage, startup, display, and USB state.\n")
	case "doctor":
		fmt.Fprint(writer, "Usage: vaultkey doctor\n\nRead-only connection and authorization checks.\n")
	case "time":
		fmt.Fprint(writer, `Usage:
  vaultkey time get [--json]
  vaultkey time set [--unix SECONDS | --rfc3339 TIME] [--json]

The device stores UTC. --rfc3339 accepts an RFC3339 timestamp with an offset.
Without a value, the current host time is used.
`)
	case "list":
		fmt.Fprint(writer, `Usage: vaultkey list [--query TEXT] [--offset N] [--limit 1-25] [--all] [--json]

Lists service, issuer, and account labels only. It never returns usernames,
passwords, notes, or secret keys. --all follows pagination.
`)
	case "add-credential":
		fmt.Fprint(writer, `Usage: vaultkey add-credential --service TEXT [options]

Options:
  --service TEXT        Required service name (max 64 bytes)
  --username TEXT       Optional username (max 128 bytes)
  --url TEXT            Optional URL (max 192 bytes)
  --notes TEXT          Optional notes (max 512 bytes)
  --password-stdin      Read one password line from stdin instead of prompting
  --json                Print {"entry_id":N}

Without --password-stdin, the password is prompted twice with echo disabled.
There is deliberately no --password flag, to keep secrets out of shell history
and process listings.
`)
	case "update-credential":
		fmt.Fprint(writer, `Usage: vaultkey update-credential ID --service TEXT [options]

Options are the same as add-credential, including the required password. This
is a complete replacement: omitted username, URL, or notes fields are cleared.
`)
	case "add-totp":
		fmt.Fprint(writer, `Usage: vaultkey add-totp [--uri-stdin | --uri-file PATH] [--json]

Without an input option, the URI is prompted with echo disabled. The URI is
limited to 1024 bytes and must start with otpauth://totp/.
`)
	case "delete":
		fmt.Fprint(writer, `Usage: vaultkey delete --type credential|totp --id N [--yes]

The label is looked up and the action is confirmed interactively. --yes skips
confirmation for non-interactive use.
`)
	case "lock":
		fmt.Fprint(writer, "Usage: vaultkey lock [--json]\n\nLocks the vault, cancels HID output, and revokes CDC authorization.\n")
	case "watch":
		fmt.Fprint(writer, "Usage: vaultkey watch [--interval DUR] [--json]\n\nPrints status at each interval as JSON Lines with --json.\n")
	default:
		printUsage(writer)
	}
}
