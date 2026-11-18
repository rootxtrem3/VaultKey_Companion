package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/apperr"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

const (
	exitOK                 = vaultkey.ExitOK
	exitFailure            = vaultkey.ExitFailure
	exitUsage              = vaultkey.ExitUsage
	exitConnection         = vaultkey.ExitConnection
	exitLocked             = vaultkey.ExitLocked
	exitNotAuthorized      = vaultkey.ExitNotAuthorized
	exitProtocol           = vaultkey.ExitProtocol
	exitAmbiguousOrTimeout = vaultkey.ExitAmbiguousOrTimeout
	exitDeviceOperation    = vaultkey.ExitDeviceOperation
	exitInterrupted        = vaultkey.ExitInterrupted
)

const defaultTimeout = 5 * time.Second

type app struct {
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	version   string
	commit    string
	buildDate string

	port       string
	timeout    time.Duration
	debug      bool
	jsonErrors bool
	command    string
	ctx        context.Context
}

type globalOptions struct {
	port       string
	timeout    time.Duration
	debug      bool
	jsonErrors bool
	version    bool
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
		case argument == "--json-errors" || argument == "-json-errors":
			options.jsonErrors = true
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
		if containsArgument(remaining, "--json") {
			printVersionJSON(stdout, version, commit, buildDate)
		} else {
			printVersion(stdout, version, commit, buildDate)
		}
		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	application := &app{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		version:    version,
		commit:     commit,
		buildDate:  buildDate,
		port:       options.port,
		timeout:    options.timeout,
		debug:      options.debug,
		jsonErrors: options.jsonErrors,
		ctx:        ctx,
	}

	if len(remaining) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	command := remaining[0]
	arguments = remaining[1:]
	application.command = command
	switch command {
	case "help", "-h", "--help":
		if len(arguments) > 0 {
			printCommandHelp(stdout, arguments[0])
		} else {
			printUsage(stdout)
		}
		return exitOK
	case "version":
		return application.runVersion(arguments)
	case "capabilities":
		return application.runCapabilities(arguments)
	case "session":
		return application.runSession(arguments)
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
	info := apperr.Classify(err)
	if a.jsonErrors {
		payload := struct {
			OK      bool               `json:"ok"`
			Command string             `json:"command"`
			Error   vaultkey.ErrorInfo `json:"error"`
		}{OK: false, Command: a.command, Error: info}
		if writeErr := writeJSON(a.stdout, payload); writeErr != nil {
			fmt.Fprintf(a.stderr, "error: %v\n", writeErr)
		}
		return info.ExitCode
	}
	if info.Kind == "interrupted" {
		fmt.Fprintln(a.stderr, "interrupted")
		return info.ExitCode
	}
	fmt.Fprintln(a.stderr, apperr.FormatError(info))
	if info.Kind == "ambiguous" {
		fmt.Fprintf(a.stderr, "warning: %s\n", info.Hint)
	} else if info.Hint != "" {
		fmt.Fprintf(a.stderr, "hint: %s\n", info.Hint)
	}
	return info.ExitCode
}

func (a *app) openOptions(port string) vaultkey.OpenOptions {
	options := vaultkey.OpenOptions{
		Port:         port,
		ProbeTimeout: a.probeTimeout(),
		Version:      a.version,
		Commit:       a.commit,
		BuildDate:    a.buildDate,
	}
	if a.debug {
		options.Logger = func(message string) { fmt.Fprintln(a.stderr, "debug: "+message) }
	}
	return options
}

func (a *app) openWithContext(ctx context.Context) (*vaultkey.Session, error) {
	return vaultkey.Open(ctx, a.openOptions(a.port))
}

func (a *app) openPortWithContext(ctx context.Context, port string) (*vaultkey.Session, error) {
	if port == "" {
		port = a.port
	}
	return vaultkey.Open(ctx, a.openOptions(port))
}

func (a *app) connect() (*vaultkey.Session, error) {
	session, err := a.openWithContext(a.ctx)
	if err == nil && a.port == "" {
		a.debugf("auto-detected %s", session.PortName())
	}
	return session, err
}

func (a *app) probeTimeout() time.Duration {
	timeout := 2 * time.Second
	if a.timeout < timeout {
		timeout = a.timeout
	}
	return timeout
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
  --json-errors     Write one JSON error object to stdout on failure
  --version         Print the app version
  -h, --help        Show help

Commands:
  session              Run the long-lived JSON Lines UI session
  capabilities         Describe the machine interface (no device access)
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
	case "session":
		fmt.Fprint(writer, `Usage: vaultkey session [--port PATH] [--timeout DUR] [--debug]

Runs one long-lived connection and speaks JSON Lines over stdin/stdout. This is
the interface the desktop app uses: one process owns the serial port so the
firmware's five-minute CDC authorization is not revoked between commands.

stdout carries only protocol JSON; diagnostics go to stderr. The first line is
a "ready" event. Send {"id":1,"method":"capabilities"} to negotiate, then use
status, list, time.get/time.set, credentials.add/update, totp.add, delete,
lock, watch.start/watch.stop, reconnect, doctor, and shutdown.
`)
	case "capabilities":
		fmt.Fprint(writer, `Usage: vaultkey capabilities [--json]

Describes the session protocol, app platform, firmware limits, supported
methods, features, and exit codes. It never opens a serial port.
`)
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
		fmt.Fprint(writer, "Usage: vaultkey doctor [--json]\n\nRead-only connection and authorization checks. Use --json for a machine-readable report.\n")
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
	case "version":
		fmt.Fprint(writer, "Usage: vaultkey version [--json]\n\nPrints the app version. --json adds platform and protocol metadata.\n")
	default:
		printUsage(writer)
	}
}
