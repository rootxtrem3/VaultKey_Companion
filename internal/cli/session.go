package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/rootxtrem3/VaultKey_Companion/pkg/session"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

func containsArgument(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if argument == wanted {
			return true
		}
	}
	return false
}

func (a *app) runVersion(arguments []string) int {
	flags := newFlagSet("version", "Print the app version.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if a.version == "" {
		a.version = "unknown"
	}
	if *jsonOutput {
		printVersionJSON(a.stdout, a.version, a.commit, a.buildDate)
	} else {
		printVersion(a.stdout, a.version, a.commit, a.buildDate)
	}
	return exitOK
}

func printVersionJSON(writer interface{ Write([]byte) (int, error) },
	version, commit, buildDate string) {
	if commit == "" {
		commit = "unknown"
	}
	if buildDate == "" {
		buildDate = "unknown"
	}
	_ = writeJSON(writer, struct {
		App struct {
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			BuildDate string `json:"build_date"`
			Platform  string `json:"platform"`
		} `json:"app"`
		Protocol        int `json:"protocol"`
		SessionProtocol int `json:"session_protocol"`
	}{
		App: struct {
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			BuildDate string `json:"build_date"`
			Platform  string `json:"platform"`
		}{
			Version: version, Commit: commit, BuildDate: buildDate,
			Platform: runtime.GOOS + "/" + runtime.GOARCH,
		},
		Protocol:        vaultkey.ProtocolVersion,
		SessionProtocol: session.SessionProtocol,
	})
}

func (a *app) runCapabilities(arguments []string) int {
	flags := newFlagSet("capabilities", "Describe the machine interface without opening a device.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "error: capabilities takes no positional arguments")
		return exitUsage
	}
	capabilities := session.CapabilitiesFor(a.sessionOptions(nil, nil))
	if *jsonOutput {
		if err := writeJSON(a.stdout, capabilities); err != nil {
			return a.fail(err)
		}
		return exitOK
	}

	fmt.Fprintln(a.stdout, "VaultKey capabilities")
	fmt.Fprintf(a.stdout, "App:       %s (commit %s, built %s)\n",
		capabilities.App.Version, capabilities.App.Commit, capabilities.App.BuildDate)
	fmt.Fprintf(a.stdout, "Platform:  %s\n", capabilities.App.Platform)
	fmt.Fprintf(a.stdout, "Protocol:  %d\n", capabilities.Protocol)
	fmt.Fprintf(a.stdout, "Session:   %d\n", capabilities.SessionProtocol)
	fmt.Fprintf(a.stdout, "Limits:    service=%d username=%d password=%d url=%d notes=%d query=%d totp_uri=%d line=%d page=%d/%d entries=%d/%d\n",
		capabilities.Limits.Service, capabilities.Limits.Username,
		capabilities.Limits.Password, capabilities.Limits.URL,
		capabilities.Limits.Notes, capabilities.Limits.Query,
		capabilities.Limits.TOTPURI, capabilities.Limits.LineLength,
		capabilities.Limits.PageLimit, capabilities.Limits.PageOffset,
		capabilities.Limits.Credentials, capabilities.Limits.TOTP)
	fmt.Fprintln(a.stdout, "Methods:")
	for _, method := range capabilities.Methods {
		fmt.Fprintf(a.stdout, "  %s\n", method)
	}
	fmt.Fprintf(a.stdout, "Features:  %v\n", capabilities.Features)
	return exitOK
}

func (a *app) sessionOptions(connector session.Connector,
	reconnect func(context.Context, string) (session.Device, error)) session.Options {
	options := session.Options{
		Connector:      connector,
		Reconnect:      reconnect,
		Version:        a.version,
		Commit:         a.commit,
		BuildDate:      a.buildDate,
		DefaultTimeout: a.timeout,
		Debug:          a.debug,
	}
	if a.debug {
		options.Logger = func(message string) { fmt.Fprintln(a.stderr, "debug: "+message) }
	}
	return options
}

func (a *app) runSession(arguments []string) int {
	for _, argument := range arguments {
		switch argument {
		case "-h", "--help":
			printCommandHelp(a.stdout, "session")
			return exitOK
		case "--json", "--json-errors":
			// Global flags already handled; accept them defensively.
		default:
			if len(argument) > 0 && argument[0] == '-' {
				fmt.Fprintf(a.stderr, "error: unknown session option %q\n", argument)
				return exitUsage
			}
			fmt.Fprintln(a.stderr, "error: session takes no positional arguments")
			return exitUsage
		}
	}

	connector := func(ctx context.Context) (session.Device, error) {
		device, err := a.openWithContext(ctx)
		if err != nil {
			return nil, err
		}
		return device, nil
	}
	reconnect := func(ctx context.Context, port string) (session.Device, error) {
		device, err := a.openPortWithContext(ctx, port)
		if err != nil {
			return nil, err
		}
		return device, nil
	}
	return session.Run(a.ctx, a.stdin, a.stdout, a.stderr,
		a.sessionOptions(connector, reconnect))
}
