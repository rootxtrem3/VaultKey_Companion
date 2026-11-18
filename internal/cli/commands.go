package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rootxtrem3/VaultKey_Companion/internal/prompt"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

type deviceRow struct {
	Port     string `json:"port"`
	State    string `json:"state"`
	Product  string `json:"product,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Hardware string `json:"hardware,omitempty"`
	Protocol int    `json:"protocol,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (a *app) runDevices(arguments []string) int {
	flags := newFlagSet("devices", "Probe candidate serial ports for a VaultKey.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "error: devices takes no positional arguments")
		return exitUsage
	}

	rows, err := vaultkey.Discover(a.ctx, a.openOptions(a.port))
	if err != nil {
		return a.fail(err)
	}
	if rows == nil {
		rows = []vaultkey.DeviceInfo{}
	}

	if *jsonOutput {
		if err := writeJSON(a.stdout, rows); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "No candidate serial ports found.")
		fmt.Fprintln(a.stdout, "Check the cable, make sure the firmware is running, or pass --port.")
		return exitOK
	}
	writer := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "PORT\tSTATE\tPRODUCT\tFIRMWARE\tDETAIL")
	for _, row := range rows {
		detail := row.Error
		if detail == "" && row.Hardware != "" {
			detail = "hardware " + row.Hardware
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			row.Port, row.State, row.Product, row.Firmware, detail)
	}
	_ = writer.Flush()
	return exitOK
}

func (a *app) runInfo(arguments []string) int {
	flags := newFlagSet("info", "Show VaultKey device identity.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}

	device, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer device.Close()
	hello := device.Hello()

	if *jsonOutput {
		output := struct {
			Port   string         `json:"port"`
			Device vaultkey.Hello `json:"device"`
		}{Port: device.PortName(), Device: hello}
		if err := writeJSON(a.stdout, output); err != nil {
			return a.fail(err)
		}
		return exitOK
	}

	fmt.Fprintf(a.stdout, "Port:      %s\n", device.PortName())
	fmt.Fprintf(a.stdout, "Product:   %s\n", hello.Product)
	fmt.Fprintf(a.stdout, "Hardware:  %s\n", hello.Hardware)
	fmt.Fprintf(a.stdout, "Firmware:  %s\n", hello.Firmware)
	fmt.Fprintf(a.stdout, "Protocol:  %d\n", hello.Protocol)
	return exitOK
}

func (a *app) runStatus(arguments []string) int {
	flags := newFlagSet("status", "Show lock, RTC, storage, startup, and USB state.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}

	device, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer device.Close()
	hello := device.Hello()

	ctx, cancel := a.timeoutContext()
	defer cancel()
	status, err := device.Status(ctx)
	if err != nil {
		return a.fail(err)
	}

	if *jsonOutput {
		output := struct {
			Port   string          `json:"port"`
			Device vaultkey.Hello  `json:"device"`
			Status vaultkey.Status `json:"status"`
		}{Port: device.PortName(), Device: hello, Status: status}
		if err := writeJSON(a.stdout, output); err != nil {
			return a.fail(err)
		}
		return exitOK
	}

	fmt.Fprintf(a.stdout, "Port:          %s\n", device.PortName())
	fmt.Fprintf(a.stdout, "Device:        %s %s, firmware %s, protocol %d\n",
		hello.Product, hello.Hardware, hello.Firmware, hello.Protocol)
	fmt.Fprintf(a.stdout, "Locked:        %s\n", yesNo(status.Locked))
	if status.StartupError != "" {
		fmt.Fprintf(a.stdout, "Startup:       %s (error: %s)\n", status.Startup, status.StartupError)
	} else {
		fmt.Fprintf(a.stdout, "Startup:       %s\n", status.Startup)
	}
	fmt.Fprintf(a.stdout, "RTC:           %s\n", status.RTC)
	fmt.Fprintf(a.stdout, "Storage:       %s\n", status.Storage)
	fmt.Fprintf(a.stdout, "Display:       %s\n", status.Display)
	fmt.Fprintf(a.stdout, "USB driver:    %s\n", yesNo(status.USBDriver))
	fmt.Fprintf(a.stdout, "USB mounted:   %s\n", yesNo(status.USBMounted))
	fmt.Fprintf(a.stdout, "CDC:           %s\n", yesNo(status.CDC))
	fmt.Fprintf(a.stdout, "HID:           %s\n", yesNo(status.HID))
	fmt.Fprintf(a.stdout, "CDC auth:      %s\n", yesNo(status.Provisioning))
	if status.Provisioning {
		fmt.Fprintf(a.stdout, "Entries:       %d credential(s), %d TOTP\n",
			derefCount(status.Credentials), derefCount(status.TOTP))
	} else {
		fmt.Fprintln(a.stdout, "Entries:       hidden until CDC provisioning is authorized")
		if status.Locked {
			fmt.Fprintln(a.stdout, "Hint:          unlock the device, then choose USB Functions -> Authorize CDC 5 min")
		} else {
			fmt.Fprintln(a.stdout, "Hint:          choose USB Functions -> Authorize CDC 5 min on the device")
		}
	}
	return exitOK
}

func derefCount(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func (a *app) runDoctor(arguments []string) int {
	flags := newFlagSet("doctor", "Run read-only connection and authorization checks.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}

	device, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer device.Close()

	report, err := device.Doctor(a.ctx)
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, report); err != nil {
			return a.fail(err)
		}
		return exitOK
	}

	fmt.Fprintln(a.stdout, "VaultKey doctor")
	fmt.Fprintln(a.stdout)
	fmt.Fprintf(a.stdout, "[ok]   app %s on %s/%s\n", a.version, runtime.GOOS, runtime.GOARCH)
	for _, check := range report.Checks {
		fmt.Fprintf(a.stdout, "[%s]   %s\n", check.Status, check.Message)
		if check.Detail != "" {
			fmt.Fprintf(a.stdout, "       %s\n", check.Detail)
		}
	}
	return exitOK
}

func (a *app) runTime(arguments []string) int {
	if len(arguments) == 0 {
		printCommandHelp(a.stderr, "time")
		return exitUsage
	}
	subcommand := arguments[0]
	arguments = arguments[1:]
	switch subcommand {
	case "get":
		return a.runTimeGet(arguments)
	case "set":
		return a.runTimeSet(arguments)
	case "help", "-h", "--help":
		printCommandHelp(a.stdout, "time")
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "vaultkey: unknown time command %q\n", subcommand)
		printCommandHelp(a.stderr, "time")
		return exitUsage
	}
}

func (a *app) runTimeGet(arguments []string) int {
	flags := newFlagSet("time get", "Read the device UTC time.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()
	ctx, cancel := a.timeoutContext()
	defer cancel()
	clock, err := client.Time(ctx)
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, clock); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	fmt.Fprintf(a.stdout, "UTC:  %s\n", formatClock(clock))
	fmt.Fprintf(a.stdout, "Unix: %d\n", clock.Unix)
	return exitOK
}

type timeSetOutput struct {
	Before   *vaultkey.Clock `json:"before,omitempty"`
	Set      vaultkey.Clock  `json:"set"`
	Verified *vaultkey.Clock `json:"verified,omitempty"`
	Warning  string          `json:"warning,omitempty"`
}

func (a *app) runTimeSet(arguments []string) int {
	flags := newFlagSet("time set", "Set the device UTC time.", a.stderr)
	var unixValue int64
	var unixSet bool
	flags.Func("unix", "UTC Unix timestamp", func(raw string) error {
		parsed, err := parseInt64(raw)
		if err != nil {
			return err
		}
		unixValue = parsed
		unixSet = true
		return nil
	})
	rfc3339 := flags.String("rfc3339", "", "RFC3339 timestamp")
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if unixSet && *rfc3339 != "" {
		fmt.Fprintln(a.stderr, "error: use only one of --unix and --rfc3339")
		return exitUsage
	}

	target := time.Now().UTC()
	if unixSet {
		target = time.Unix(unixValue, 0).UTC()
	} else if *rfc3339 != "" {
		parsed, err := time.Parse(time.RFC3339, *rfc3339)
		if err != nil {
			fmt.Fprintf(a.stderr, "error: invalid RFC3339 time: %v\n", err)
			return exitUsage
		}
		target = parsed.UTC()
	}
	if err := vaultkey.ValidateUnix(target.Unix()); err != nil {
		return a.fail(err)
	}

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	output := timeSetOutput{}
	beforeCtx, beforeCancel := a.timeoutContext()
	before, beforeErr := client.Time(beforeCtx)
	beforeCancel()
	if beforeErr == nil {
		output.Before = &before
	}

	setCtx, setCancel := a.timeoutContext()
	setClock, err := client.SetTime(setCtx, target.Unix())
	setCancel()
	if err != nil {
		return a.fail(err)
	}
	// SET_TIME writes UTC; the firmware response omits the GET_TIME utc flag.
	setClock.UTC = true
	output.Set = setClock

	verifyCtx, verifyCancel := a.timeoutContext()
	verified, verifyErr := client.Time(verifyCtx)
	verifyCancel()
	if verifyErr == nil {
		output.Verified = &verified
	} else {
		output.Warning = "time was written but verification failed: " + verifyErr.Error()
	}

	if *jsonOutput {
		if err := writeJSON(a.stdout, output); err != nil {
			return a.fail(err)
		}
	} else {
		if output.Before != nil {
			fmt.Fprintf(a.stdout, "Before:   %s (unix %d)\n", formatClock(*output.Before), output.Before.Unix)
		} else {
			fmt.Fprintf(a.stdout, "Before:   unavailable (%v)\n", beforeErr)
		}
		fmt.Fprintf(a.stdout, "Set:      %s (unix %d)\n", formatClock(output.Set), output.Set.Unix)
		if output.Verified != nil {
			fmt.Fprintf(a.stdout, "Verified: %s (unix %d)\n", formatClock(*output.Verified), output.Verified.Unix)
		} else {
			fmt.Fprintf(a.stdout, "Warning:  %s\n", output.Warning)
		}
	}

	if verifyErr != nil {
		return exitDeviceOperation
	}
	return exitOK
}

type listOutput struct {
	CredentialTotal uint32                       `json:"credential_total"`
	TOTPTotal       uint32                       `json:"totp_total"`
	Credentials     []vaultkey.CredentialSummary `json:"credentials"`
	TOTP            []vaultkey.TOTPSummary       `json:"totp"`
	All             bool                         `json:"all"`
	Offset          uint32                       `json:"offset,omitempty"`
	Limit           uint32                       `json:"limit,omitempty"`
}

func (a *app) runList(arguments []string) int {
	flags := newFlagSet("list", "List credential and TOTP labels.", a.stderr)
	query := flags.String("query", "", "case-insensitive label filter")
	offset := flags.Uint("offset", 0, "pagination offset")
	limit := flags.Uint("limit", 20, "page size (1-25)")
	all := flags.Bool("all", false, "follow pagination")
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if *limit < 1 || *limit > vaultkey.MaxPageLimit {
		fmt.Fprintf(a.stderr, "error: --limit must be 1-%d\n", vaultkey.MaxPageLimit)
		return exitUsage
	}
	if *offset > vaultkey.MaxPageOffset {
		fmt.Fprintf(a.stderr, "error: --offset must be 0-%d\n", vaultkey.MaxPageOffset)
		return exitUsage
	}
	if *all && *offset != 0 {
		fmt.Fprintln(a.stderr, "error: --all cannot be combined with --offset")
		return exitUsage
	}

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	result := listOutput{All: *all}
	if *all {
		aggregate, err := client.ListAll(a.ctx, *query, a.timeout)
		if err != nil {
			return a.fail(err)
		}
		result.CredentialTotal = aggregate.CredentialTotal
		result.TOTPTotal = aggregate.TOTPTotal
		result.Credentials = aggregate.Credentials
		result.TOTP = aggregate.TOTP
	} else {
		ctx, cancel := a.timeoutContext()
		page, err := client.List(ctx, *query, uint32(*offset), uint32(*limit))
		cancel()
		if err != nil {
			return a.fail(err)
		}
		result.CredentialTotal = page.CredentialTotal
		result.TOTPTotal = page.TOTPTotal
		result.Credentials = page.Credentials
		result.TOTP = page.TOTP
		result.Offset = page.Offset
		result.Limit = page.Limit
	}
	if result.Credentials == nil {
		result.Credentials = []vaultkey.CredentialSummary{}
	}
	if result.TOTP == nil {
		result.TOTP = []vaultkey.TOTPSummary{}
	}

	if *jsonOutput {
		if err := writeJSON(a.stdout, result); err != nil {
			return a.fail(err)
		}
		return exitOK
	}

	fmt.Fprintf(a.stdout, "Credentials (%d):\n", result.CredentialTotal)
	if len(result.Credentials) == 0 {
		fmt.Fprintln(a.stdout, "  none")
	} else {
		writer := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "  ID\tSERVICE")
		for _, entry := range result.Credentials {
			fmt.Fprintf(writer, "  %d\t%s\n", entry.ID, entry.Service)
		}
		_ = writer.Flush()
	}
	fmt.Fprintf(a.stdout, "TOTP (%d):\n", result.TOTPTotal)
	if len(result.TOTP) == 0 {
		fmt.Fprintln(a.stdout, "  none")
	} else {
		writer := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "  ID\tISSUER\tACCOUNT")
		for _, entry := range result.TOTP {
			fmt.Fprintf(writer, "  %d\t%s\t%s\n", entry.ID, entry.Issuer, entry.Account)
		}
		_ = writer.Flush()
	}
	if !*all {
		shownCredentials := result.Offset + uint32(len(result.Credentials))
		shownTOTP := result.Offset + uint32(len(result.TOTP))
		if shownCredentials < result.CredentialTotal || shownTOTP < result.TOTPTotal {
			fmt.Fprintln(a.stdout, "More entries exist; use --all or adjust --offset/--limit.")
		}
	}
	return exitOK
}

func (a *app) runAddCredential(arguments []string) int {
	flags := newFlagSet("add-credential", "Add a credential.", a.stderr)
	service := flags.String("service", "", "service name (required)")
	username := flags.String("username", "", "username")
	url := flags.String("url", "", "URL")
	notes := flags.String("notes", "", "notes")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from stdin")
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "error: unexpected positional argument")
		return exitUsage
	}

	input := vaultkey.CredentialInput{
		Service:  *service,
		Username: *username,
		URL:      *url,
		Notes:    *notes,
	}
	// Reject bad non-secret fields before prompting, so a typo cannot consume
	// a password and then fail local validation.
	if err := vaultkey.ValidateCredentialFields(input); err != nil {
		return a.fail(err)
	}
	password, err := a.readCredentialPassword(*passwordStdin, true)
	if err != nil {
		return a.fail(err)
	}
	input.Password = password
	if err := vaultkey.ValidateCredential(input); err != nil {
		return a.fail(err)
	}
	a.warnHID(input)

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	ctx, cancel := a.timeoutContext()
	id, err := client.AddCredential(ctx, input)
	cancel()
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, map[string]uint32{"entry_id": id}); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	fmt.Fprintf(a.stdout, "Added credential #%d.\n", id)
	return exitOK
}

func (a *app) runUpdateCredential(arguments []string) int {
	var id uint32
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		parsed, err := parseUint32(arguments[0])
		if err != nil {
			fmt.Fprintf(a.stderr, "error: invalid entry ID %q\n", arguments[0])
			return exitUsage
		}
		id = parsed
		arguments = arguments[1:]
	}

	flags := newFlagSet("update-credential", "Replace a credential by ID.", a.stderr)
	service := flags.String("service", "", "service name (required)")
	username := flags.String("username", "", "username (empty clears)")
	url := flags.String("url", "", "URL (empty clears)")
	notes := flags.String("notes", "", "notes (empty clears)")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from stdin")
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if id == 0 && flags.NArg() == 1 {
		parsed, err := parseUint32(flags.Arg(0))
		if err != nil {
			fmt.Fprintf(a.stderr, "error: invalid entry ID %q\n", flags.Arg(0))
			return exitUsage
		}
		id = parsed
	} else if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "error: unexpected positional argument")
		return exitUsage
	}
	if id == 0 {
		fmt.Fprintln(a.stderr, "error: entry ID is required")
		return exitUsage
	}

	input := vaultkey.CredentialInput{
		Service:  *service,
		Username: *username,
		URL:      *url,
		Notes:    *notes,
	}
	// Reject bad non-secret fields before prompting, so a typo cannot consume
	// a password and then fail local validation.
	if err := vaultkey.ValidateCredentialFields(input); err != nil {
		return a.fail(err)
	}
	password, err := a.readCredentialPassword(*passwordStdin, true)
	if err != nil {
		return a.fail(err)
	}
	input.Password = password
	if err := vaultkey.ValidateCredential(input); err != nil {
		return a.fail(err)
	}
	a.warnHID(input)

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	ctx, cancel := a.timeoutContext()
	updated, err := client.UpdateCredential(ctx, id, input)
	cancel()
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, map[string]uint32{"entry_id": updated}); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	fmt.Fprintf(a.stdout, "Updated credential #%d.\n", updated)
	return exitOK
}

func (a *app) runAddTOTP(arguments []string) int {
	flags := newFlagSet("add-totp", "Import an otpauth://totp/ URI.", a.stderr)
	uriStdin := flags.Bool("uri-stdin", false, "read the URI from stdin")
	uriFile := flags.String("uri-file", "", "read the URI from a file")
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if *uriStdin && *uriFile != "" {
		fmt.Fprintln(a.stderr, "error: use only one of --uri-stdin and --uri-file")
		return exitUsage
	}

	var uri string
	var err error
	switch {
	case *uriStdin:
		uri, err = readOneLine(a.stdin, vaultkey.MaxTOTPURI+1)
	case *uriFile != "":
		uri, err = readFileLine(*uriFile, vaultkey.MaxTOTPURI+1)
	default:
		uri, err = prompt.Secret("TOTP URI: ")
	}
	if err != nil {
		return a.fail(err)
	}
	uri = strings.TrimSpace(uri)
	if err := vaultkey.ValidateTOTPURI(uri); err != nil {
		return a.fail(err)
	}

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	ctx, cancel := a.timeoutContext()
	id, err := client.AddTOTP(ctx, uri)
	cancel()
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, map[string]uint32{"entry_id": id}); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	fmt.Fprintf(a.stdout, "Added TOTP #%d.\n", id)
	return exitOK
}

func (a *app) runDelete(arguments []string) int {
	flags := newFlagSet("delete", "Delete a credential or TOTP entry.", a.stderr)
	entryType := flags.String("type", "", "credential or totp")
	idValue := flags.Uint("id", 0, "entry ID")
	yes := flags.Bool("yes", false, "skip confirmation")
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "error: unexpected positional argument")
		return exitUsage
	}
	if *entryType != string(vaultkey.CredentialEntry) &&
		*entryType != string(vaultkey.TOTPEntry) {
		fmt.Fprintln(a.stderr, `error: --type must be "credential" or "totp"`)
		return exitUsage
	}
	if *idValue == 0 || uint64(*idValue) > math.MaxUint32 {
		fmt.Fprintln(a.stderr, "error: --id must be a positive 32-bit value")
		return exitUsage
	}
	id := uint32(*idValue)

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	label := fmt.Sprintf("%s #%d", *entryType, id)
	if !*yes {
		found, findErr := a.findLabel(client, vaultkey.EntryType(*entryType), id)
		if findErr != nil {
			if vaultkey.IsCode(findErr, "provisioning_not_authorized") ||
				vaultkey.IsCode(findErr, "device_locked") {
				return a.fail(findErr)
			}
			fmt.Fprintf(a.stderr, "warning: could not look up the label: %v\n", findErr)
		} else if found != "" {
			label = fmt.Sprintf("%s #%d %q", *entryType, id, found)
		}
		confirmed, confirmErr := prompt.Confirm("Delete " + label + "?")
		if confirmErr != nil {
			return a.fail(confirmErr)
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled.")
			return exitOK
		}
	}

	ctx, cancel := a.timeoutContext()
	err = client.Delete(ctx, vaultkey.EntryType(*entryType), id)
	cancel()
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, map[string]any{
			"deleted": true,
			"type":    *entryType,
			"id":      id,
		}); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	fmt.Fprintf(a.stdout, "Deleted %s #%d.\n", *entryType, id)
	return exitOK
}

func (a *app) findLabel(client *vaultkey.Session, entryType vaultkey.EntryType,
	id uint32) (string, error) {
	list, err := client.ListAll(a.ctx, "", a.timeout)
	if err != nil {
		return "", err
	}
	if entryType == vaultkey.CredentialEntry {
		for _, entry := range list.Credentials {
			if entry.ID == id {
				return entry.Service, nil
			}
		}
		return "", nil
	}
	for _, entry := range list.TOTP {
		if entry.ID == id {
			label := entry.Issuer
			if entry.Account != "" {
				label += ": " + entry.Account
			}
			return label, nil
		}
	}
	return "", nil
}

func (a *app) runLock(arguments []string) int {
	flags := newFlagSet("lock", "Lock the vault and revoke CDC authorization.", a.stderr)
	jsonOutput := flags.Bool("json", false, "print JSON")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	ctx, cancel := a.timeoutContext()
	err = client.Lock(ctx)
	cancel()
	if err != nil {
		return a.fail(err)
	}
	if *jsonOutput {
		if err := writeJSON(a.stdout, map[string]bool{"locked": true}); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	fmt.Fprintln(a.stdout, "Vault locked; CDC authorization revoked.")
	return exitOK
}

func (a *app) runWatch(arguments []string) int {
	flags := newFlagSet("watch", "Poll device status until interrupted.", a.stderr)
	interval := flags.Duration("interval", 2*time.Second, "poll interval")
	jsonOutput := flags.Bool("json", false, "print JSON Lines")
	ok, code := parseFlags(flags, arguments)
	if !ok {
		return code
	}
	if *interval < 250*time.Millisecond || *interval > time.Minute {
		fmt.Fprintln(a.stderr, "error: --interval must be between 250ms and 1m")
		return exitUsage
	}

	client, err := a.connect()
	if err != nil {
		return a.fail(err)
	}
	defer client.Close()

	for {
		ctx, cancel := a.timeoutContext()
		status, err := client.Status(ctx)
		cancel()
		if err != nil {
			return a.fail(err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if *jsonOutput {
			line := struct {
				Time   string          `json:"time"`
				Status vaultkey.Status `json:"status"`
			}{Time: now, Status: status}
			if err := writeJSONLine(a.stdout, line); err != nil {
				return a.fail(err)
			}
		} else {
			entries := "hidden"
			if status.Provisioning {
				entries = fmt.Sprintf("%d cred/%d totp",
					derefCount(status.Credentials), derefCount(status.TOTP))
			}
			fmt.Fprintf(a.stdout,
				"%s locked=%s rtc=%q storage=%q auth=%s cdc=%s hid=%s entries=%s\n",
				now, yesNo(status.Locked), status.RTC, status.Storage,
				yesNo(status.Provisioning), yesNo(status.CDC), yesNo(status.HID), entries)
		}

		timer := time.NewTimer(*interval)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return a.fail(a.ctx.Err())
		case <-timer.C:
		}
	}
}

func (a *app) readCredentialPassword(fromStdin, confirm bool) (string, error) {
	if fromStdin {
		return readOneLine(a.stdin, 4096)
	}
	first, err := prompt.Secret("Password: ")
	if err != nil {
		return "", err
	}
	if confirm {
		second, err := prompt.Secret("Confirm password: ")
		if err != nil {
			return "", err
		}
		if first != second {
			return "", &vaultkey.RequestError{Message: "passwords do not match"}
		}
	}
	return first, nil
}

func (a *app) warnHID(input vaultkey.CredentialInput) {
	if !vaultkey.HIDTypeable(input.Username) || !vaultkey.HIDTypeable(input.Password) {
		fmt.Fprintln(a.stderr,
			"warning: username or password contains non-ASCII/control bytes; "+
				"the device can store it but its HID keyboard cannot type it")
	}
}

func readOneLine(reader io.Reader, maximum int) (string, error) {
	buffered := bufio.NewReaderSize(reader, maximum+2)
	var line []byte
	for {
		chunk, err := buffered.ReadSlice('\n')
		line = append(line, chunk...)

		if newline := bytes.IndexByte(line, '\n'); newline >= 0 {
			return lineValue(line[:newline], maximum)
		}
		// No terminator yet. A pending CR is not content, so a CRLF split
		// across reads does not count against the limit.
		if len(bytes.TrimSuffix(line, []byte{'\r'})) > maximum {
			return "", &vaultkey.RequestError{Message: "input line is too long"}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return "", &vaultkey.RequestError{Message: "no input received"}
			}
			return lineValue(line, maximum)
		}
		if err != nil {
			return "", err
		}
	}
}

// lineValue enforces the content limit after removing a single trailing CR.
// A trailing LF is already excluded by the caller, so a line whose content is
// exactly maximum bytes is accepted.
func lineValue(content []byte, maximum int) (string, error) {
	content = bytes.TrimSuffix(content, []byte{'\r'})
	if len(content) > maximum {
		return "", &vaultkey.RequestError{Message: "input line is too long"}
	}
	return string(content), nil
}

func readFileLine(path string, maximum int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return readOneLine(file, maximum)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSONLine(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func formatClock(clock vaultkey.Clock) string {
	return time.Unix(clock.Unix, 0).UTC().Format(time.RFC3339)
}

func parseInt64(raw string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", raw)
	}
	return value, nil
}

func parseUint32(raw string) (uint32, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid unsigned integer %q", raw)
	}
	return uint32(value), nil
}
