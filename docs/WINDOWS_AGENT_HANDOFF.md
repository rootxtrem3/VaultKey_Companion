# VaultKey Companion — Windows Agent Handoff

**Status:** implementation brief. The repository is at v0.1.0 and does **not** yet contain the session/UI work described here.
**Audience:** an AI coding agent working on a Windows machine.
**Goal:** make the Go companion safe for a Windows UI to drive, expose a public Go API, and prepare for a later Windows-only DLL/API.

**Do not modify** the `VaultKey_Firmware`, PCB, or enclosure projects. This work is confined to `VaultKey_Companion`.

---

## 0. TL;DR

1. **The UI must use one long-lived connection.** Start `vaultkey session` once and speak JSON Lines over stdin/stdout. Closing a COM port drops DTR, and the rev-D firmware revokes the 5-minute CDC authorization when that happens. A UI that spawns one CLI process per command will force the user to re-authorize on the device constantly.
2. **Fix the Windows idle-read bug before building the session.** The current Windows serial code returns `(0, nil)` on read timeout; `bufio.Reader` converts 100 consecutive empty reads into `io.ErrNoProgress`, so an idle connection dies after roughly 10 seconds. This already breaks `vaultkey watch --interval 30s` on Windows.
3. Implement the public packages (`pkg/vaultkey`, `pkg/session`), the session protocol, structured JSON errors, `capabilities`, and `doctor --json`.
4. Verify with Go tests, Windows cross-builds/vet, and the Linux/WSL PTY integration suite. **No Wine.** Real Windows/hardware behavior still has to be validated on a physical rev-D unit.

Read this whole document before changing code. Then follow the phase order.

---

## 1. Repository and environment

### 1.1 Source of truth

| Item | Location |
|---|---|
| Companion app (this repo) | `VaultKey_Companion` |
| Firmware (read-only reference) | `VaultKey_Firmware` |
| Protocol authority | `VaultKey_Firmware/docs/USB_PROTOCOL.md` |
| Firmware limits | `VaultKey_Firmware/include/core/HardwareConfig.h` |
| Authorization behavior | `VaultKey_Firmware/src/core/Application.cpp` |
| USB CDC/DTR behavior | `VaultKey_Firmware/src/hardware/USBComposite.cpp`, Arduino `USBCDC.cpp` |

The Linux origin path was `/home/rootx/Projects/VaultKey_Companion`. On Windows, use whatever directory the repo is copied to; all paths below are relative to the repo root.

### 1.2 Toolchain

- Go **1.24 or newer** (`go.mod` declares `go 1.24`; the Linux validation used `go1.27.4`).
- PowerShell 7+ preferred; Windows PowerShell 5.1 works but UTF-8 handling is worse.
- Git recommended. The project is **not** a Git repository yet in the Linux origin.
- Optional: Python 3 (docs/tools only), WSL (for the PTY integration suite).
- No network dependencies are allowed. The app must remain standard-library only.

### 1.3 First 30 minutes

```powershell
# From the repo root
go version
go env GOOS GOARCH CGO_ENABLED
go test ./... -count=1
go vet ./...
go build -trimpath -o bin\vaultkey.exe .\cmd\vaultkey
.\bin\vaultkey.exe version
.\bin\vaultkey.exe devices --json
```

If the directory is not a Git repo:

```powershell
git init
git add .
git commit -m "chore: baseline VaultKey companion v0.1.0"
```

Do not commit `bin/` or `dist/`; `.gitignore` already excludes them.

Create a branch for the work:

```powershell
git switch -c feature/windows-ui-ready
```

---

## 2. Absolute constraints

These are not negotiable.

1. **No firmware, PCB, or enclosure changes.** Do not edit the other projects. Read them for protocol/behavior only.
2. **No third-party Go dependencies.** Use the standard library and `syscall`/`unsafe` on Windows. Do not add `golang.org/x/sys`, `go.bug.st/serial`, `tarm/serial`, etc.
3. **No fake device in production code.** Test simulators/fakes are allowed only in `_test.go` files or `test/`.
4. **No automatic retry of mutating commands.** A timeout or write failure after sending `ADD_*`, `UPDATE_*`, `DELETE_*`, `SET_TIME`, or `LOCK_DEVICE` is an ambiguous outcome. Report it and let the caller verify.
5. **No secrets in argv.** Keep the existing no-`--password` / no-`--uri` rule. Secrets enter through hidden prompt, stdin, file, or the session JSON body.
6. **No secret logging.** `--debug` may log command names, byte counts, and status only. It must never log request bodies, passwords, TOTP secrets, or response bodies.
7. **Do not use Wine.** Cross-compile and run native Windows tests where possible. PTY integration tests are Unix/WSL-only.
8. **Do not claim hardware validation without evidence.** The origin environment had no rev-D board. If the Windows machine also has none, say so explicitly.

---

## 3. Current state (v0.1.0)

### 3.1 What exists

```text
cmd/vaultkey/main.go              process entry point, ldflags version
internal/cli/                     parsing, commands, output, exit codes
internal/vaultkey/                protocol client, typed API, validation, limits
internal/transport/               serial: Linux/macOS termios, Windows DCB, discovery
internal/prompt/                  hidden terminal input and confirmation
test/integration/                 PTY simulator, CLI checks, hidden-prompt checks
docs/TEST_REPORT.md               verification record and gaps
scripts/build-release.sh          six cross-builds + SHA256SUMS
Makefile                          check/integration/release
```

Current CLI commands:

`devices`, `info`, `status`, `doctor`, `time get`, `time set`, `list`, `add-credential`, `update-credential`, `add-totp`, `delete`, `lock`, `watch`, `version`, `help`.

Current global options:

`--port`, `--timeout` (500ms–1m, default 5s), `--debug`, `--version`, `-h/--help`. `VAULTKEY_PORT` and `VAULTKEY_TIMEOUT` env fallbacks exist.

### 3.2 Current verification

From the Linux origin:

- `gofmt` clean, `go vet ./...` pass.
- 30 unit-test functions pass, including `-race`.
- PTY CLI integration: 55/55 checks pass.
- PTY hidden-prompt integration: 18/18 pass.
- Six cross-builds pass: linux/darwin/windows × amd64/arm64.
- `dist/SHA256SUMS` verifies.
- **No physical rev-D hardware was available.**
- **Windows and macOS binaries were cross-compiled but not executed.**

Re-run everything on Windows and report what actually ran.

### 3.3 Current device/protocol limits

Mirror these exactly; do not invent new limits.

| Field | Limit |
|---|---:|
| Service | 64 bytes |
| Username | 128 bytes |
| Password | 256 bytes |
| URL | 192 bytes |
| Notes | 512 bytes |
| Query | 64 bytes |
| TOTP URI | 1024 bytes |
| Request line | 2048 bytes before LF |
| Page limit | 25 |
| Page offset | 256 |
| Credentials | 128 |
| TOTP entries | 128 |

Firmware entry IDs are random non-zero 32-bit values, not sequential.

### 3.4 Authorization model

Allowed while locked:

`HELLO`, `GET_VERSION`, `GET_STATUS`, `GET_TIME`, `LOCK_DEVICE`.

Everything else requires:

1. The vault is unlocked with the on-device PIN.
2. The host has the CDC port open (DTR + RTS asserted).
3. The user selected **USB Functions -> Authorize CDC 5 min** on the device.
4. The 5-minute deadline has not expired.

The app cannot unlock the vault or extend the window.

---

## 4. Why the current CLI is not UI-ready

### 4.1 Closing the port revokes authorization

Firmware evidence:

- `Application.cpp:142-144` calls `revokeProvisioning()` whenever `provisioningUntilMs_ != 0 && !provisioningAuthorized()`.
- `Application.cpp:272-276` requires `authentication_.unlocked() && usb_.cdcConnected() && deadline`.
- `USBComposite.cpp:66-68` defines `cdcConnected()` as `driverStarted_ && static_cast<bool>(cdc_)`.
- Arduino `USBCDC::operator bool()` returns `connected`.
- `USBCDC::_onLineState` sets `connected = true` only for DTR && RTS, and false when DTR drops.

Therefore:

- Each CLI invocation opens the port, sends one command, closes the port, and drops DTR.
- Windows COM ports are opened exclusively (`CreateFile` share mode `0`), so a second process cannot hold the port open while another sends a command.
- A Windows UI cannot use one process per command and keep the 5-minute window alive.

**Required design:** one long-lived `vaultkey session` process owns the port. The UI starts it first, then asks the user to unlock and authorize on the device, then sends all operations through that session.

### 4.2 Windows idle reads disconnect after ~10 seconds

- `internal/transport/serial_windows.go` configures `COMMTIMEOUTS` with `ReadTotalTimeoutConstant: 100`.
- Windows `ReadFile` returns `(0, nil)` after the timeout when no bytes are available.
- `internal/vaultkey/client.go` wraps the port in `bufio.NewReaderSize`.
- `bufio.Reader` retries empty reads and after `maxConsecutiveEmptyReads = 100` returns `io.ErrNoProgress`.
- 100 × ~100ms ≈ 10 seconds of idle before the client tears down the connection.

This already breaks `vaultkey watch --interval 30s` on Windows and would break any long-lived UI session.

**Required fix:** an idle-tolerant reader in the client (or equivalent) that loops on `(0, nil)` with a short wait and returns only on data, error, or close. Test it with a fake port that returns many empty reads.

### 4.3 Machine-interface gaps

- `fail()` writes prose to stderr. There is no stable JSON error object or machine code.
- No `capabilities` command and no `version --json`.
- `doctor` has no `--json`.
- Windows discovery reads all of `SERIALCOMM` and probes every port with `HELLO`; Linux discovery filters by USB VID `303a`.
- Windows port sorting is lexical, so `COM10` sorts before `COM2`.
- Port-busy is not distinguished from permission denied.
- No session/agent mode.

### 4.4 Protocol metadata limitation

`LIST_SERVICES` returns only:

- credential: `id`, `service`
- TOTP: `id`, `issuer`, `account`

There is no command to read back a credential's username, URL, notes, password, or TOTP secret. A UI can add, fully replace, and delete entries, but cannot pre-fill an edit form from the device. This is a firmware/protocol limitation; do not try to work around it by modifying firmware.

---

## 5. Target architecture

### 5.1 Package layout

```text
cmd/vaultkey/            process entry, ldflags
pkg/vaultkey/            public device API: Open, Session, types, validation, errors, discovery
pkg/session/             public JSON Lines session engine for CLI and future DLL
internal/transport/      serial ports and platform discovery
internal/prompt/         hidden terminal input
internal/apperr/         error -> stable kind/code/hint/exit mapping
internal/cli/            argument parsing, formatting, command wiring
test/integration/        PTY simulator checks (Unix/WSL)
test/windows/            PowerShell smoke script and manual checklist
docs/UI_INTEGRATION.md   UI contract, lifecycle, examples, DLL notes
```

### 5.2 Dependency rules

```text
pkg/vaultkey   -> internal/transport, stdlib
pkg/session    -> pkg/vaultkey, internal/apperr, stdlib
internal/apperr-> pkg/vaultkey, internal/transport, internal/prompt
internal/cli   -> pkg/vaultkey, pkg/session, internal/apperr,
                  internal/transport, internal/prompt
cmd/vaultkey   -> internal/cli
```

No cycles. `pkg/vaultkey` must not import `internal/prompt` or `internal/cli`. `pkg/session` must not import `internal/cli`.

External users can import `pkg/vaultkey` and `pkg/session`; the internal imports are resolved inside this module. If the code is later copied to a separate DLL repo, `internal/apperr` must move with it or be inlined.

### 5.3 Why `pkg/` and not `internal/`

The future Windows DLL should be able to wrap the same device/session code. `internal/vaultkey` can only be imported by this module; promoting it to `pkg/vaultkey` makes the API reusable without changing behavior. There are no external users of the current internal package, so the move is safe.

---

## 6. Implementation plan

Work in the order below. Keep commits small and run `go test ./...` after each phase.

### Phase 0 — Baseline and public package move

#### 0.1 Move the device package

Move:

```text
internal/vaultkey/*.go  ->  pkg/vaultkey/*.go
```

Update imports in `internal/cli`, tests, and any other file:

```text
github.com/rootxtrem3/VaultKey_Companion/internal/vaultkey
->
github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey
```

Run:

```powershell
gofmt -w .
go test ./...
go vet ./...
```

Acceptance:

- No references to `internal/vaultkey` remain.
- All existing tests pass.
- `vaultkey` CLI behavior is unchanged.

#### 0.2 Add `Open`, `Discover`, and a `Session`

Create `pkg/vaultkey/open.go`:

```go
type OpenOptions struct {
    Port         string        // empty = auto-detect
    ProbeTimeout time.Duration // default 2s, capped by caller timeout
    Logger       func(string)
}

type DeviceInfo struct {
    Port     string `json:"port"`
    State    string `json:"state"` // vaultkey | other-device | no-response | unreachable
    Product  string `json:"product,omitempty"`
    Firmware string `json:"firmware,omitempty"`
    Hardware string `json:"hardware,omitempty"`
    Protocol int    `json:"protocol,omitempty"`
    Error    string `json:"error,omitempty"`
}

func Open(ctx context.Context, options OpenOptions) (*Session, error)
func Discover(ctx context.Context, options OpenOptions) ([]DeviceInfo, error)
```

Move the existing detection/probing logic out of `internal/cli` (`detectPort`, `probe`, `probeResult`, `notFoundError`, `multipleDevicesError`) into `pkg/vaultkey`.

Behavior:

- `Port == ""`: enumerate candidates, probe each with `HELLO`, require exactly one `Product == "VaultKey"`.
- No candidate/response: return `*NotFoundError` with the per-port results.
- More than one VaultKey: return `*MultipleDevicesError`.
- Explicit `Port`: open it, send `HELLO`, require `Product == "VaultKey"` and `Protocol == 1`.
- `Discover` probes the given port or all candidates and returns rows; it does not fail just because a port is not a VaultKey.
- `Open` must close the port on any validation failure.

Add a `Session` wrapper:

```go
type Session struct {
    // client *Client, hello Hello
}

func (s *Session) Close() error
func (s *Session) PortName() string
func (s *Session) Hello() Hello
func (s *Session) Client() *Client // optional; for advanced use
func (s *Session) ListAll(ctx context.Context, query string, pageTimeout time.Duration) (ServiceList, error)
func (s *Session) Doctor(ctx context.Context) (DoctorReport, error)
```

`ListAll` must move the pagination/dedupe logic currently in `internal/cli.fetchAll` so the CLI and session share it. Set the result's `Offset` and `Limit` explicitly and document them.

Acceptance:

- `Discover` and `Open` have unit tests for: no ports, multiple VaultKeys, a non-VaultKey device, protocol mismatch, explicit port.
- Use an unexported `openPort`/`candidates` hook in `pkg/vaultkey` tests so tests can run without hardware.
- CLI `devices` and `info` use the new functions and produce the same output.

#### 0.3 Add public error info and exit codes

Add to `pkg/vaultkey`:

```go
type ErrorInfo struct {
    Kind       string `json:"kind"`
    Code       string `json:"code"`
    Message    string `json:"message"`
    Hint       string `json:"hint,omitempty"`
    ExitCode   int    `json:"exit_code"`
    Ambiguous  bool   `json:"ambiguous,omitempty"`
    DeviceCode string `json:"device_code,omitempty"`
    Detail     string `json:"detail,omitempty"`
}

type Classified interface {
    error
    Kind() string
    Code() string
    Hint() string
    ExitCode() int
}

const (
    ExitOK                 = 0
    ExitFailure            = 1
    ExitUsage              = 2
    ExitConnection         = 3
    ExitLocked             = 4
    ExitNotAuthorized      = 5
    ExitProtocol           = 6
    ExitAmbiguousOrTimeout = 7
    ExitDeviceOperation    = 8
    ExitInterrupted        = 130
)
```

Implement `Classified` on `NotFoundError` and `MultipleDevicesError`. Keep the existing `ProtocolError`, `DeviceError`, `AmbiguousError`, `RequestError`, `ErrClosed`.

Add a `Limits` value:

```go
type Limits struct {
    Service     int `json:"service"`
    Username    int `json:"username"`
    Password    int `json:"password"`
    URL         int `json:"url"`
    Notes       int `json:"notes"`
    Query       int `json:"query"`
    TOTPURI     int `json:"totp_uri"`
    LineLength  int `json:"request_line"`
    PageLimit   int `json:"page_limit"`
    PageOffset  int `json:"page_offset"`
    Credentials int `json:"credentials"`
    TOTP        int `json:"totp"`
}
```

Add `MaxCredentials = 128` and `MaxTOTP = 128` constants.

Acceptance:

- Exit code values match the current CLI values exactly.
- Tests cover `Classified` on the discovery errors.

#### 0.4 Add a public Doctor report

Add `pkg/vaultkey/doctor.go`:

```go
type Check struct {
    Name    string `json:"name"`
    Status  string `json:"status"` // ok | warn | fail
    Message string `json:"message"`
    Detail  string `json:"detail,omitempty"`
}

type DoctorReport struct {
    OK       bool      `json:"ok"`
    App      AppInfo   `json:"app"`
    Platform string    `json:"platform"`
    Port     string    `json:"port,omitempty"`
    Device   *Hello    `json:"device,omitempty"`
    Checks   []Check   `json:"checks"`
}

type AppInfo struct {
    Version   string `json:"version"`
    Commit    string `json:"commit"`
    BuildDate string `json:"build_date"`
    Platform  string `json:"platform"`
}
```

`Session.Doctor` runs read-only checks:

- port selected/opened,
- `HELLO` identity and protocol,
- `GET_STATUS`,
- locked/unlocked,
- CDC provisioning authorized,
- RTC status and `GET_TIME`,
- storage status,
- USB mounted/CDC/HID,
- platform note about exclusive COM ports and authorization loss on close.

It must never mutate. `OK` is true when no `fail` check exists. Warnings do not fail the process; the CLI's `doctor` exit code remains the current behavior (connection failure = 3, otherwise 0).

Acceptance:

- Unit tests with a fake device for pass/warn/fail combinations.
- No secrets in the report.

---

### Phase A — Windows transport correctness

#### A.1 Idle-tolerant reader

Problem: `bufio.Reader` turns repeated `(0, nil)` reads into `io.ErrNoProgress`.

Implement in `pkg/vaultkey/client.go` (or a small file):

```go
type idleReader struct {
    port io.Reader
    done <-chan struct{}
}

func (r idleReader) Read(p []byte) (int, error) {
    for {
        n, err := r.port.Read(p)
        if n > 0 || err != nil {
            return n, err
        }
        select {
        case <-r.done:
            return 0, io.EOF
        case <-time.After(5 * time.Millisecond):
        }
    }
}
```

Use it as the underlying reader for `bufio.NewReaderSize`. Keep the sleep short (5–10ms) and make it a package variable or field so tests can set it to 0.

Add `Client.Done() <-chan struct{}` and `Client.Err() error` accessors. `Err()` should return `ErrClosed` after an intentional close, and the read error otherwise.

Add a unit test where a fake port returns `(0, nil)` 150 times and then a valid response. The old code would fail after 100; the new code must decode the response.

Acceptance:

- Test passes.
- Existing close/timeout tests still pass.
- `go test -race` passes.
- `vaultkey watch --interval 30s` is no longer structurally doomed on Windows (hardware test still needed).

#### A.2 Windows serial hardening

Modify `internal/transport/serial_windows.go`:

- Keep `CreateFile` exclusive (`share mode 0`) and `\\.\COMx` prefixing for `COMx`.
- Trim whitespace from the port name. Accept `COM5`, `com5`, and `\\.\COM5`.
- Keep DTR and RTS asserted:
  - `fBinary = 1`
  - `fDtrControl = DTR_CONTROL_ENABLE`
  - `fRtsControl = RTS_CONTROL_ENABLE`
  - 8 data bits, no parity, 1 stop bit
  - no flow control, no parity checking, no abort-on-error
- Increase idle read timeout so idle sessions do not poll aggressively:
  - `ReadIntervalTimeout: 50`
  - `ReadTotalTimeoutMultiplier: 0`
  - `ReadTotalTimeoutConstant: 250` (or 500; document)
  - `WriteTotalTimeoutMultiplier: 0`
  - `WriteTotalTimeoutConstant: 5000`
- Keep `CancelIoEx` before `CloseHandle`.
- Map these to `io.EOF`:
  - `ERROR_OPERATION_ABORTED` (995)
  - `ERROR_INVALID_HANDLE` (6)
  - `ERROR_HANDLE_EOF` (38)
  - `ERROR_BROKEN_PIPE` (109)
  - `ERROR_DEVICE_NOT_CONNECTED` (1167)
  - `ERROR_BAD_COMMAND` (22), if it indicates a vanished device
- Distinguish busy from permission:
  - `ERROR_ACCESS_DENIED` (5) on COM open is usually “port in use”.
  - `ERROR_SHARING_VIOLATION` (32) is definitely “in use”.
  - Return a typed `PortBusyError` with `Name` and `Unwrap`.
- Do not toggle DTR/RTS manually. The ESP32 Arduino core includes an esptool-style reset sequence: `!DTR&&RTS` -> `DTR&&RTS` -> `DTR&&!RTS` -> `!DTR&&!RTS` can restart into the bootloader. Normal open/close with both high then both low does not, but do not experiment with line-state sequences.

Add `internal/transport/serial_windows_test.go` (build tag `windows`):

- `unsafe.Sizeof(dcb{}) == 28`.
- `normalizePortName("com5")`, `normalizePortName("COM10")`, `normalizePortName("\\.\COM5")`.
- Error mapping helper.

Add `internal/transport/port.go` (no build tag) helpers:

```go
func sortPortNames([]string) []string // natural sort + dedupe
func IsBusy(err error) bool
```

`IsBusy` is platform-specific:
- Unix: `syscall.EBUSY` (and maybe `EACCES` is permission, not busy).
- Windows: `ERROR_ACCESS_DENIED`, `ERROR_SHARING_VIOLATION`.

Use `sortPortNames` in Linux, macOS, and Windows discovery.

Natural sort requirements:

- `COM2` before `COM10`.
- `/dev/ttyACM2` before `/dev/ttyACM10`.
- Stable ordering for non-numeric names.
- Deduplicate exact strings; Windows can normalize case before dedupe.

Acceptance:

- `GOOS=windows GOARCH=amd64 go vet ./...`
- `GOOS=windows GOARCH=arm64 go vet ./...`
- `GOOS=windows GOARCH=amd64 go build ./...`
- Linux tests pass.
- `port_test.go` covers COM/USB natural sorting and dedupe on Linux.

#### A.3 Windows VID-filtered discovery

Modify `internal/transport/discover_windows.go`.

Current behavior: read `HARDWARE\DEVICEMAP\SERIALCOMM`, return every value. This opens/probes unrelated hardware.

New behavior:

1. Enumerate `HKLM\SYSTEM\CurrentControlSet\Enum\USB` subkeys whose names start with `VID_303A` (case-insensitive). Examples:
   - `VID_303A&PID_0002`
   - `VID_303A&PID_0002&MI_00`
2. For each matching key, enumerate its instance subkeys.
3. Open `<instance>\Device Parameters` and read the `PortName` value (`REG_SZ`).
4. Collect non-empty names.
5. If the USB walk fails or returns zero ports, fall back to the existing `SERIALCOMM` enumeration.
6. Normalize, dedupe, and natural-sort.

Notes:

- Do **not** hardcode the PID. The Arduino ESP32 core defaults to VID `0x303A` and PID `0x0002`, but board configuration may change it.
- `SERIALCOMM` fallback preserves compatibility if the registry layout differs.
- `--port COMx` always wins; no discovery needed.
- `devices` still sends `HELLO` to each candidate, so the VID filter is a narrowing step, not trust.
- Do not add SetupAPI/cgo; use `syscall` registry APIs (`RegOpenKeyEx`, `RegEnumKeyEx`, `RegQueryValueEx`, `RegCloseKey`).
- Handle `ERROR_NO_MORE_ITEMS` (259) as end-of-enumeration.
- Use UTF-16 buffers correctly; `RegEnumKeyEx` length is in characters, `RegQueryValueEx` byte length is in bytes.
- Free/close every key.

Acceptance:

- Windows build/vet passes.
- Fallback path is tested by temporarily forcing the USB walk to fail (unit test hook) and verifying SERIALCOMM is used.
- `devices --json` on a machine with no VaultKey returns an empty array, not an error.
- A machine with a non-303A serial device does not open it during auto-detect.

#### A.4 Busy/permission hints

Update `internal/apperr` (see Phase C) so:

- `transport.IsBusy(err)` -> `kind:"connection"`, `code:"port_busy"`, exit 3, hint: “close the other program using this COM port; Windows COM ports are exclusive.”
- `os.IsPermission(err)` -> `kind:"connection"`, `code:"permission_denied"`, exit 3. On Windows, mention Device Manager/driver; on Linux, mention `dialout`/`uucp`.
- `transport.IsNotSerial(err)` -> `code:"not_serial"`.
- `*os.PathError` -> `code:"port_open_failed"`.

---

### Phase B — Public session engine and CLI session mode

#### B.1 `pkg/session` API

Create `pkg/session` with:

```go
type Device interface {
    PortName() string
    Hello() vaultkey.Hello
    Close() error
    Status(ctx context.Context) (vaultkey.Status, error)
    Time(ctx context.Context) (vaultkey.Clock, error)
    SetTime(ctx context.Context, unix int64) (vaultkey.Clock, error)
    List(ctx context.Context, query string, offset, limit uint32) (vaultkey.ServiceList, error)
    ListAll(ctx context.Context, query string, pageTimeout time.Duration) (vaultkey.ServiceList, error)
    AddCredential(ctx context.Context, input vaultkey.CredentialInput) (uint32, error)
    UpdateCredential(ctx context.Context, id uint32, input vaultkey.CredentialInput) (uint32, error)
    AddTOTP(ctx context.Context, uri string) (uint32, error)
    Delete(ctx context.Context, entryType vaultkey.EntryType, id uint32) error
    Lock(ctx context.Context) error
    Doctor(ctx context.Context) (vaultkey.DoctorReport, error)
}

type Connector func(ctx context.Context) (Device, error)

type Options struct {
    Connector      Connector
    Version        string
    Commit         string
    BuildDate      string
    DefaultTimeout time.Duration
    Debug          bool
    Logger         func(string)
}

type Engine struct { /* ... */ }

func New(ctx context.Context, options Options) (*Engine, error)
func (e *Engine) Handle(ctx context.Context, line []byte, emit func([]byte) error) error
func (e *Engine) Close() error

func Run(ctx context.Context, in io.Reader, out, errOut io.Writer, options Options) int
```

`*vaultkey.Session` must satisfy `Device`.

`Engine` responsibilities:

- Own the current `Device` and a `Connector`.
- Serialize requests; one in-flight. A second concurrent request gets `busy`.
- Decode one JSON request object, dispatch, encode one response.
- Keep `watch` state and a ticker goroutine; emit status events through `emit`.
- Ensure `emit` is safe for concurrent calls (wrap it in a mutex inside `Engine`, or require the caller to provide a safe function and document it; wrapping is safer).
- Never retry a mutating request.
- Redact secrets in debug logs.

`Run` responsibilities:

- Create the engine via `New`.
- Emit `ready`.
- Read JSON Lines from `in`; write responses/events to `out`; diagnostics to `errOut`.
- Exit on stdin EOF (0), `shutdown` (0), context cancel/interrupt (130), or fatal startup error (classified exit code).
- Flush each output line.
- Limit request lines to 64 KiB. On an oversized line, emit an error and resynchronize at the next newline.
- Treat a write failure to stdout as “UI gone”: close and exit without spamming stderr.

#### B.2 CLI wiring

Add `internal/cli/session.go` and a `session` command in `cli.go`:

```go
func (a *app) runSession(arguments []string) int
```

Parse only `--help` (global `--port`, `--timeout`, `--debug` already work). Call:

```go
return session.Run(a.ctx, a.stdin, a.stdout, a.stderr, session.Options{
    Connector: func(ctx context.Context) (session.Device, error) {
        return a.openWithContext(ctx)
    },
    Version:        a.version,
    Commit:         a.commit,
    BuildDate:      a.buildDate,
    DefaultTimeout: a.timeout,
    Debug:          a.debug,
    Logger:         func(message string) { fmt.Fprintln(a.stderr, "debug: "+message) },
})
```

`a.openWithContext` uses `pkg/vaultkey.Open` with the supplied context, so startup/reconnect probes honor cancellation.

Do not make `session` open the port if `--help` is requested.

#### B.3 Session protocol v1

This is the contract the UI and the future DLL will use. Implement it exactly; update `docs/UI_INTEGRATION.md` with the final text.

Transport:

- stdin/stdout, UTF-8, one JSON object per line.
- stdout contains **only** protocol JSON. Diagnostics go to stderr.
- Requests are processed serially. One in-flight.
- `id` is a required unsigned integer, echoed in the response. If omitted/zero, use 0 and still respond.
- `timeout_ms` is optional, 100–120000. Default: `--timeout`. Clamp.
- Unknown fields are ignored for forward compatibility.
- Unknown methods return `unknown_method` and keep the session alive.
- Malformed JSON returns `invalid_json` and keeps the session alive.

Startup event:

```json
{"type":"event","event":"ready","result":{"session_protocol":1,"app":{"version":"0.2.0","commit":"abc1234","build_date":"2026-11-16T12:00:00Z","platform":"windows/amd64"},"port":"COM5","device":{"product":"VaultKey","firmware":"1.0.0","hardware":"rev-D","protocol":1}}}
```

Request:

```json
{"id":1,"method":"status","params":{},"timeout_ms":5000}
```

Success:

```json
{"type":"response","id":1,"ok":true,"result":{...}}
```

Failure:

```json
{"type":"response","id":2,"ok":false,"error":{"kind":"device","code":"device_locked","message":"the device is locked","hint":"unlock the device with its PIN, then retry.","exit_code":4,"ambiguous":false,"device_code":"device_locked"}}
```

Events:

```json
{"type":"event","event":"status","result":{"time":"2026-11-16T12:00:00Z","status":{...}}}
{"type":"event","event":"watch_error","error":{...}}
{"type":"event","event":"disconnected","error":{...}}
{"type":"event","event":"fatal","error":{...}}
```

`fatal` is only for startup/connection failures that prevent the session from running; the process exits with `error.exit_code`.

Methods:

| Method | Params | Result |
|---|---|---|
| `capabilities` | none | `Capabilities` (below) |
| `hello` | none | `{"port":"COM5","device":{...}}` |
| `status` | none | `vaultkey.Status` |
| `time.get` | none | `{"unix":N,"utc":bool}` |
| `time.set` | `{"unix":N}` or `{"rfc3339":"..."}` | `{"before":Clock|null,"set":Clock,"verified":Clock|null,"warning":"..."}` |
| `list` | `{"query":"","offset":0,"limit":20,"all":false}` | `vaultkey.ServiceList` |
| `credentials.add` | `{"service":"...","username":"...","password":"...","url":"...","notes":"..."}` | `{"entry_id":N}` |
| `credentials.update` | `{"id":N,"service":"...","username":"...","password":"...","url":"...","notes":"..."}` | `{"entry_id":N}` |
| `totp.add` | `{"uri":"otpauth://totp/..."}` | `{"entry_id":N}` |
| `delete` | `{"type":"credential"|"totp","id":N}` | `{"deleted":true,"type":"...","entry_id":N}` |
| `lock` | none | `{"locked":true}` |
| `watch.start` | `{"interval_ms":2000}` | `{"watching":true,"interval_ms":2000}` |
| `watch.stop` | none | `{"watching":false}` |
| `reconnect` | `{"port":"COM7"}` optional | `{"port":"COM7","device":{...}}` |
| `shutdown` | none | `{"shutting_down":true}`, then exit 0 |
| `doctor` | none | `vaultkey.DoctorReport` |

Method details:

- `capabilities` does not touch the device. It reports app/platform/protocol/limits/methods/features/exit codes.
- `hello` returns the cached identity from startup/reconnect.
- `status` is allowed while locked/unauthorized.
- `time.get` is allowed while locked; `time.set` requires unlock + authorization.
- `list` uses `ListAll` when `all` is true. Document `offset`/`limit` in the aggregated result.
- `credentials.add`/`update` validate all fields locally before sending. Password is required. Update is a full replacement; omitted optional fields clear them.
- `totp.add` validates the URI prefix/length locally; the device is the authority on Base32/parameters.
- `delete` requires `id > 0` and a valid type. It does not prompt; the UI must confirm.
- `lock` revokes CDC authorization. The session stays open; subsequent management commands fail with `provisioning_not_authorized` until the user re-authorizes on the device.
- `watch.start` replaces the interval if already watching. Emit a status event immediately, then every interval. Range 250–60000ms.
- `watch.stop` stops the ticker; existing queued event may still arrive.
- `reconnect` closes the current device and runs the connector again. It is explicit and never automatic. If the port is fixed and the device re-enumerated on a different COM port, pass the new port or restart the session.
- `shutdown` responds first, closes the port, and exits 0.
- No method prompts. No method reads the terminal.

`capabilities` shape:

```json
{
  "session_protocol": 1,
  "app": {"version":"0.2.0","commit":"abc1234","build_date":"2026-11-16T12:00:00Z","platform":"windows/amd64"},
  "protocol": 1,
  "limits": {"service":64,"username":128,"password":256,"url":192,"notes":512,"query":64,"totp_uri":1024,"request_line":2048,"page_limit":25,"page_offset":256,"credentials":128,"totp":128},
  "methods": ["capabilities","hello","status","time.get","time.set","list","credentials.add","credentials.update","totp.add","delete","lock","watch.start","watch.stop","reconnect","shutdown","doctor"],
  "features": ["session","watch","json_errors","capabilities","doctor"],
  "exit_codes": {"ok":0,"failure":1,"usage":2,"connection":3,"locked":4,"not_authorized":5,"protocol":6,"ambiguous_or_timeout":7,"device_operation":8,"interrupted":130}
}
```

#### B.4 Secret handling in the session

- Accept `password` and `uri` as JSON strings in the request body. This is not argv and is not visible in process listings.
- Never log request bodies. If `--debug` is on, log only method, request size, duration, and status.
- Do not echo secrets in errors. Error messages may name fields and limits, never values.
- Go strings are immutable; do not claim zeroization. Document that host memory is not a secure enclave, same as the CLI.
- Validate lengths before dispatch. Reject invalid UTF-8.
- Keep secrets out of `capabilities`, `status`, `list`, `doctor`, and all events.

Acceptance for Phase B:

- Unit tests with a fake `Device` cover every method, error mapping, busy behavior, malformed JSON, oversized lines, watch start/stop, reconnect, and shutdown.
- A test asserts the fake device's mutating method is called exactly once on an ambiguous error (no retry).
- A test asserts `emit` is serialized.
- A test asserts no secret appears in debug output/errors.
- CLI `session` works against the PTY simulator: one open, several commands, one authorization state.
- `shutdown` and stdin EOF exit 0 and close the port.
- Ctrl-C exits 130 and closes the port.

---

### Phase C — Machine-readable one-shot interface

#### C.1 `internal/apperr`

Create `internal/apperr/apperr.go`:

```go
func Classify(err error) vaultkey.ErrorInfo
```

Mapping:

| Condition | Kind | Code | Exit |
|---|---|---|---:|
| `context.Canceled` | `interrupted` | `interrupted` | 130 |
| `*vaultkey.AmbiguousError` | `ambiguous` | `ambiguous_write` | 7 |
| `*vaultkey.RequestError` | `usage` | `invalid_request` | 2 |
| `*vaultkey.ProtocolError` | `protocol` | `protocol_error` | 6 |
| `*vaultkey.DeviceError` | `device` | device code | mapped below |
| `prompt.ErrNoTerminal` | `usage` | `no_terminal` | 2 |
| `context.DeadlineExceeded` | `timeout` | `timeout` | 7 |
| `vaultkey.ErrClosed` | `connection` | `connection_closed` | 3 |
| `transport.ErrUnsupported` | `connection` | `unsupported_platform` | 3 |
| `transport.IsBusy(err)` | `connection` | `port_busy` | 3 |
| `os.IsPermission(err)` | `connection` | `permission_denied` | 3 |
| `*os.PathError` | `connection` | `port_open_failed` | 3 |
| `*vaultkey.NotFoundError` | `connection` | `device_not_found` | 3 |
| `*vaultkey.MultipleDevicesError` | `connection` | `multiple_devices` | 3 |
| `transport.IsNotSerial(err)` | `connection` | `not_serial` | 3 |
| `vaultkey.Classified` | use its values | use its values | use its values |
| default | `internal` | `internal_error` | 1 |

Device code mapping (preserve current messages/hints):

- `device_locked` -> `kind:"locked"`, code `device_locked`, exit 4.
- `provisioning_not_authorized` -> `kind:"not_authorized"`, code `provisioning_not_authorized`, exit 5.
- `rtc_time_invalid` -> `kind:"device"`, exit 8.
- `storage_failed`, `delete_failed`, `entry_not_found` -> `kind:"device"`, exit 8.
- `invalid_credential`, `invalid_query`, `invalid_pagination`, `invalid_time`, `invalid_uri`, `invalid_totp`, `invalid_entry_type` -> `kind:"usage"`, exit 2.
- Unknown device code -> `kind:"device"`, exit 1.

Set `DeviceCode` and `Detail` for device errors. Set `Ambiguous: true` only for ambiguous outcomes. Keep hints identical to the current `fail()` text where possible.

Refactor `internal/cli.fail` to use `apperr.Classify` and preserve human output:

- `error: <message>` plus ` (<device code>)` for device errors.
- `hint: ...` when present.
- `warning: ...` for ambiguous outcomes.
- The exact human strings can change slightly, but preserve the integration checks (`may have been applied`, `non-ASCII`, etc.).

Acceptance:

- Unit tests table-drive every mapping.
- No prose leaks to stdout in JSON mode.
- Exit codes are unchanged.

#### C.2 `--json-errors`

Add a global flag `--json-errors` and an `app.jsonErrors` field.

When enabled, `fail(err)` writes exactly one JSON object to **stdout**:

```json
{"ok":false,"command":"list","error":{...}}
```

and exits with `error.exit_code`. Do not also write prose to stderr; minimal diagnostics are okay only if writing JSON fails. If the command already streamed human output (`watch`), document that `--json`/session is the clean machine path. For normal one-shot commands, nothing is printed before the final result, so this is safe.

`--json-errors` is independent of `--json`. The UI should use both:

```powershell
.\vaultkey.exe --json-errors list --json
```

Acceptance:

- Tests for a successful command (unchanged JSON) and failures at each exit-code class.
- `watch` behavior documented.

#### C.3 `version --json`

`vaultkey version --json` (and `vaultkey --version --json` if easy) prints:

```json
{"app":{"version":"0.2.0","commit":"...","build_date":"...","platform":"windows/amd64"},"protocol":1,"session_protocol":1}
```

No device access. Keep the human output unchanged without `--json`.

#### C.4 `capabilities`

Add `vaultkey capabilities [--json]`.

- No device access.
- JSON shape as in B.3.
- Human output: a readable list of protocol, session protocol, limits, and methods.
- Include `features` and `exit_codes`.

This lets the UI negotiate before touching hardware.

#### C.5 `doctor --json`

Add `--json` to `runDoctor`.

- Build a `vaultkey.DoctorReport` using the open session.
- Output the report.
- Exit code: connection failure = 3; otherwise 0. `report.OK` is false if any check is `fail`.
- Human output remains the current `[ok]/[warn]/[fail]` format.

Acceptance:

- `doctor --json` with no device returns a JSON report/error and exit 3.
- `doctor --json` with a fake authorized device returns the expected checks.

---

### Phase D — Integration tests, Windows smoke, docs, release

#### D.1 Unit tests to add

- `pkg/vaultkey`:
  - `Open`/`Discover` not found, multiple, foreign product, protocol mismatch.
  - `ListAll` pagination/dedupe.
  - `Doctor` pass/warn/fail.
  - idle reader.
- `pkg/session`:
  - protocol framing, request IDs, unknown method, invalid JSON, oversized line.
  - every method with a fake device.
  - busy behavior.
  - ambiguous mutating error, no retry.
  - watch start/stop, event serialization.
  - reconnect, shutdown.
  - secret redaction.
  - capabilities shape.
- `internal/apperr`: full mapping table.
- `internal/transport`: natural sort, dedupe, busy detection, Windows-only DCB/normalization/error mapping.
- `internal/cli`: `--json-errors`, `capabilities`, `version --json`, `doctor --json`.

#### D.2 PTY integration (Linux or WSL)

The existing simulator is `test/integration/sim.py`, which uses `pty` and is Unix-only. Add `test/integration/check_session.py` (or extend `check_cli.sh`) to:

1. Start `vaultkey session --port $PTY` with pipes.
2. Read the `ready` event.
3. Send `status`, `list`, `time get`, `capabilities`; assert responses.
4. Set simulator state to locked/unauthorized; assert `device_locked`/`provisioning_not_authorized`.
5. `watch.start` with 250ms; read at least two `status` events; `watch.stop`.
6. Send malformed JSON; assert `invalid_json`; then send a valid request and assert the session still works.
7. Send an oversized line; assert error and resync.
8. Trigger the simulator's `drop` for a mutating command; assert `ambiguous:true` and that the simulator received exactly one command.
9. `shutdown`; assert exit 0 and the PTY is closed.
10. Restart and close stdin (EOF); assert exit 0 and the port closes.
11. Assert the simulator saw exactly one open for the multi-command session.
12. Assert authorization remains true across commands (simulator state).

Update `test/integration/run.sh` and `test/integration/README.md`.

If Python PTY is unavailable on Windows, run this under WSL or Linux; document that it did not run natively on Windows.

#### D.3 Windows smoke script

Create `test/windows/smoke.ps1` and `test/windows/README.md`.

The script should:

- Accept `-Exe`, `-Port` (optional), `-Interactive` (optional).
- Run `capabilities --json`, `version --json`, `devices --json`.
- Start a session with:
  - `UseShellExecute = $false`
  - `CreateNoWindow = $true`
  - `RedirectStandardInput/Output/Error = $true`
  - UTF-8 encodings without BOM
- Send `capabilities`, `status`, `hello`, `watch.start` (250ms), `watch.stop`, `shutdown`.
- Print/parse each response and fail on `ok:false` unless expected.
- If `-Interactive`, prompt on the device for unlock/authorize and exercise `list`, add/update/delete, TOTP, lock.
- Never print secrets.
- Ensure the child process is killed on script exit.

Add a manual checklist (see Appendix D).

#### D.4 Windows build checks

Add a `windows-check` target to the Makefile (for Git Bash/WSL) and a `scripts/check.ps1` for native Windows:

```powershell
go test ./... -count=1
go vet ./...
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
foreach ($arch in @("amd64","arm64")) {
    $env:GOARCH = $arch
    go build ./...
    go vet ./...
}
Remove-Item Env:GOOS, Env:GOARCH
```

Add `GOOS=windows GOARCH=amd64/arm64 go vet ./...` and `go build ./...` to `make check` if the environment supports it. Do not run `go test` cross-compiled; it cannot execute.

`go test -race` on Windows requires cgo and a C compiler. If unavailable, run the race suite on Linux/WSL and document it.

#### D.5 Docs

Create `docs/UI_INTEGRATION.md` with:

- The authorization lifecycle (start session -> unlock -> authorize -> operate -> re-authorize).
- Why one process per command is wrong on Windows.
- The full session protocol (methods, params, results, events, errors).
- The JSON error envelope and exit codes.
- Threading: one in-flight, `busy`, watch events, UI must serialize.
- Timeouts and ambiguous outcomes; never auto-retry mutating commands.
- Secrets: send in JSON over the pipe, never argv; redaction; memory caveat.
- Reconnect/unplug behavior.
- Process lifecycle: spawn, UTF-8, no console window, kill on exit, job object.
- PowerShell and C# examples (see Appendix C).
- DLL notes: session process now; `-buildmode=c-shared` later requires cgo + a Windows C toolchain; do not assume the current `CGO_ENABLED=0` pipeline can produce it.
- The `LIST_SERVICES` metadata limitation.

Update:

- `README.md`: link to `docs/UI_INTEGRATION.md`; Windows section; session mode; exclusivity; UTF-8; no-console operation.
- `docs/TEST_REPORT.md`: new verification results, Windows cross-build/vet, session integration, explicit untested items.
- `test/integration/README.md`: session checks.
- Command help text: `session`, `capabilities`, `--json-errors`, `doctor --json`.
- `Makefile`/`scripts/build-release.sh`: version `0.2.0`; Windows check target.
- `cmd/vaultkey/main.go`: default version `0.2.0`.

#### D.6 Release

Target `0.2.0`.

```powershell
$env:CGO_ENABLED = "0"
$targets = @("linux/amd64","linux/arm64","darwin/amd64","darwin/arm64","windows/amd64","windows/arm64")
foreach ($t in $targets) {
    $os, $arch = $t.Split("/")
    $env:GOOS = $os; $env:GOARCH = $arch
    $suffix = if ($os -eq "windows") { ".exe" } else { "" }
    $out = "dist\vaultkey_0.2.0_${os}_${arch}${suffix}"
    go build -trimpath -ldflags "-s -w -X main.version=0.2.0 -X main.commit=$($env:COMMIT) -X main.buildDate=$($env:BUILD_DATE)" -o $out .\cmd\vaultkey
}
Get-ChildItem dist\vaultkey_* | Get-FileHash -Algorithm SHA256 |
    ForEach-Object { "{0}  {1}" -f $_.Hash.ToLower(), (Split-Path $_.Path -Leaf) } |
    Set-Content dist\SHA256SUMS -Encoding ascii
```

Verify with `Get-FileHash`/`certutil` or on Linux with `sha256sum -c`.

---

## 7. Public API contract (summary)

### 7.1 `pkg/vaultkey`

```go
type OpenOptions struct {
    Port         string
    ProbeTimeout time.Duration
    Logger       func(string)
}

func Open(ctx context.Context, options OpenOptions) (*Session, error)
func Discover(ctx context.Context, options OpenOptions) ([]DeviceInfo, error)

type Session struct{ /* ... */ }

func (s *Session) Close() error
func (s *Session) PortName() string
func (s *Session) Hello() Hello
func (s *Session) Status(ctx context.Context) (Status, error)
func (s *Session) Time(ctx context.Context) (Clock, error)
func (s *Session) SetTime(ctx context.Context, unix int64) (Clock, error)
func (s *Session) List(ctx context.Context, query string, offset, limit uint32) (ServiceList, error)
func (s *Session) ListAll(ctx context.Context, query string, pageTimeout time.Duration) (ServiceList, error)
func (s *Session) AddCredential(ctx context.Context, input CredentialInput) (uint32, error)
func (s *Session) UpdateCredential(ctx context.Context, id uint32, input CredentialInput) (uint32, error)
func (s *Session) AddTOTP(ctx context.Context, uri string) (uint32, error)
func (s *Session) Delete(ctx context.Context, entryType EntryType, id uint32) error
func (s *Session) Lock(ctx context.Context) error
func (s *Session) Doctor(ctx context.Context) (DoctorReport, error)
```

Stability:

- The exported types/JSON tags are part of the UI contract. Do not rename fields after 0.2.0 without a session-protocol version bump.
- `Client` may remain exported for tests/advanced use, but external users should use `Session`/`Open`.
- Contexts: callers own deadlines. `Session` does not silently override them.

### 7.2 `pkg/session`

```go
type Device interface { /* as in B.1 */ }
type Connector func(ctx context.Context) (Device, error)

type Options struct {
    Connector      Connector
    Version        string
    Commit         string
    BuildDate      string
    DefaultTimeout time.Duration
    Debug          bool
    Logger         func(string)
}

func New(ctx context.Context, options Options) (*Engine, error)
func (e *Engine) Handle(ctx context.Context, line []byte, emit func([]byte) error) error
func (e *Engine) Close() error

func Run(ctx context.Context, in io.Reader, out, errOut io.Writer, options Options) int
```

The DLL can use `Engine.Handle` directly and marshal the `Response`/`Event` values itself. `Run` is the stdio wrapper.

---

## 8. Error and exit contract

### 8.1 Exit codes

| Code | Meaning |
|---:|---|
| 0 | success |
| 1 | general/internal failure |
| 2 | usage or local input error |
| 3 | connection/discovery/open failure |
| 4 | device locked |
| 5 | CDC provisioning not authorized |
| 6 | protocol error |
| 7 | ambiguous/timeout |
| 8 | device operation failure |
| 130 | interrupted (Ctrl-C) |

These values must not change.

### 8.2 Local error codes

Use these stable `code` values in `ErrorInfo`:

`interrupted`, `ambiguous_write`, `invalid_request`, `invalid_json`, `unknown_method`, `invalid_params`, `missing_field`, `value_too_long`, `invalid_utf8`, `invalid_id`, `invalid_time`, `invalid_uri`, `busy`, `timeout`, `protocol_error`, `no_terminal`, `connection_closed`, `unsupported_platform`, `port_busy`, `permission_denied`, `port_open_failed`, `device_not_found`, `multiple_devices`, `not_serial`, `internal_error`.

### 8.3 Device error codes

Pass through the firmware's codes:

`invalid_json`, `line_too_long`, `unsupported_protocol`, `missing_command`, `unknown_command`, `device_locked`, `provisioning_not_authorized`, `rtc_time_invalid`, `invalid_time`, `invalid_query`, `invalid_pagination`, `invalid_credential`, `invalid_uri`, `invalid_totp`, `entry_not_found`, `invalid_entry_type`, `storage_failed`, `delete_failed`.

Set `kind` per the mapping in C.1 and `device_code` to the firmware code.

### 8.4 Ambiguity rule

- Mutating commands: `ADD_CREDENTIAL`, `UPDATE_CREDENTIAL`, `ADD_TOTP`, `DELETE_ENTRY`, `SET_TIME`, `LOCK_DEVICE`.
- A timeout or write failure after the request is sent -> `AmbiguousError` -> `kind:"ambiguous"`, `ambiguous:true`, exit 7.
- The UI must verify with read-only commands (`status`, `list`, `time.get`) and must never blind-retry.
- Read-only timeouts are not ambiguous.

---

## 9. Windows implementation guide

### 9.1 Serial port

Use the in-box `usbser.sys` driver; no custom INF.

Required behavior:

- Port names: `COM5`, `COM10`, `\\.\COM5`. Prefix `\\.\` for `COMx`.
- Open exclusively (share mode 0).
- Binary raw mode, 8N1, no flow control.
- DTR and RTS asserted for the whole connection.
- Do not toggle DTR/RTS in an esptool reset pattern.
- Read with finite timeouts plus the client's idle-tolerant reader.
- Write timeout 5s; partial writes are completed by `writeAll`.
- `CancelIoEx` then `CloseHandle` on close.
- Map vanished-handle errors to EOF.
- Report busy separately from permission.

Do not use `FILE_FLAG_OVERLAPPED` unless you rewrite the read/write path for overlapped I/O. The current synchronous path plus `CancelIoEx` is acceptable; the idle reader makes it safe.

### 9.2 Discovery

Preferred: USB enumeration registry walk for VID `303A`.
Fallback: `SERIALCOMM` + `HELLO` probing.
Always send `HELLO` before trusting a candidate.
`--port` bypasses discovery.

Why: opening unrelated COM ports can disturb Bluetooth modems, virtual serial ports, and other hardware. Linux already filters by VID; Windows should too.

### 9.3 Console and prompts

- Session mode never reads the terminal. It uses only stdin/stdout pipes.
- One-shot hidden prompts use `CONIN$` (`internal/prompt/secret_windows.go`). This fails cleanly with `ErrNoTerminal` when no console is attached.
- A UI must use `--password-stdin` or session JSON, never prompts.
- One-shot human output is UTF-8. Modern Windows Terminal handles it. If `cmd.exe` shows mojibake, use `chcp 65001` or Windows Terminal. Do **not** call `SetConsoleOutputCP` globally; it can affect the parent console. Session/pipe output is unaffected.

### 9.4 Process lifecycle for the UI

- Start the session with `UseShellExecute = false`, `CreateNoWindow = true`, redirected stdin/stdout/stderr.
- Set UTF-8 encodings without BOM.
- Read stdout asynchronously; never block the UI thread.
- Serialize requests; wait for the matching response ID.
- Keep one session per device. Windows COM ports are exclusive.
- On UI exit, close stdin (`shutdown` first if possible) and terminate the child. A Windows Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` is the robust approach.
- If the UI dies, the child sees stdin EOF and closes the port.
- Do not run the session under a shell (`cmd.exe /c`); spawn the exe directly.
- Do not use `vaultkey watch` as a substitute for a session. It only polls and cannot accept commands.

### 9.5 Authorization lifecycle for the UI

1. Start `vaultkey session --port COM5`. The port opens and DTR/RTS assert; the device sees CDC connected.
2. Ask the user to unlock the vault with the PIN on the device.
3. Ask the user to choose **USB Functions -> Authorize CDC 5 min**.
4. Poll `status` until `provisioning: true`.
5. Perform operations.
6. After 5 minutes (or after `lock`), `status` reports `provisioning: false`; ask the user to re-authorize. The session stays open, so the device can authorize again.
7. On exit, close the session; the device loses CDC authorization, which is expected.

Do not try to extend the 5-minute window from the host; the firmware enforces it.

### 9.6 Windows runtime checklist

- [ ] `go test ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `GOOS=windows GOARCH=amd64/arm64 go build ./...` passes.
- [ ] `vaultkey.exe capabilities --json` does not open a port.
- [ ] `vaultkey.exe devices --json` works with no device (empty array) and with a device.
- [ ] `vaultkey.exe info/status` works.
- [ ] Session opens once; multiple requests succeed.
- [ ] Idle session > 30s still responds (zero-read regression).
- [ ] `watch --interval 30s` survives on Windows (zero-read regression).
- [ ] `--json-errors` emits valid JSON for exit 2/3/4/5/6/7/8.
- [ ] `doctor --json` works.
- [ ] Port-busy error is clear.
- [ ] COM10+ opens.
- [ ] Unplug/replug emits `disconnected`; `reconnect` works.
- [ ] Ctrl-C exits 130 and releases the port.
- [ ] No secret appears in debug output or the process command line.

---

## 10. Testing on Windows

### 10.1 What runs natively

- All pure Go unit tests.
- Windows-specific tests with `//go:build windows` (DCB layout, name normalization, error mapping).
- `go vet` for host and cross-targets.
- Windows binaries.

### 10.2 What does not run natively

- `test/integration/sim.py` and `check_prompts.py` use Unix PTYs. Run them under WSL or Linux CI. Do not rewrite them to require `com0com`.
- `make` may not exist. Use `scripts/check.ps1` or direct `go` commands.
- `go test -race` on Windows needs cgo + a C compiler. If unavailable, run the race suite on Linux/WSL.

### 10.3 Hardware tests (required before shipping)

If a rev-D device is available:

1. Plug in; confirm Device Manager shows a COM port.
2. `devices --json` lists it; `info` returns product/firmware/hardware/protocol 1.
3. `status` while locked works; counts are hidden.
4. Start `session`; unlock on device; authorize CDC; `status` shows `provisioning:true`.
5. Leave the session idle for 60s; `status` still works and remains authorized.
6. `list`, add credential (ASCII), update, delete; verify on-device counts/labels.
7. Add TOTP; verify.
8. `lock`; verify `status` shows locked and provisioning false; management commands exit 4/5.
9. Re-authorize while the session is open; management works again.
10. Wait for the 5-minute expiry; confirm `provisioning:false`; re-authorize.
11. Close the session; confirm the device no longer shows authorization.
12. `watch --interval 30s` for at least 2 minutes; no disconnect.
13. Unplug/replug; `disconnected` event; `reconnect` (or restart with the new COM port).
14. Open the port with another terminal; `info` reports `port_busy`.
15. Use a COM port >= 10.
16. Add a non-ASCII credential; verify the HID warning.
17. Trigger an ambiguous mutating timeout (e.g., unplug mid-write); verify exit 7/`ambiguous:true` and no automatic retry.
18. Verify HID typing of an ASCII credential.
19. Verify the 128+128 pagination boundaries if practical.
20. Check that secrets never appear in `--debug`, session logs, or the process command line.

If no device is available, mark these as blocked. Do not mark the project “hardware verified.”

---

## 11. Build and release on Windows

### 11.1 Development

```powershell
go build -trimpath -o bin\vaultkey.exe .\cmd\vaultkey
go test ./... -count=1
go vet ./...
```

### 11.2 Cross-build matrix

```powershell
$env:CGO_ENABLED = "0"
$version = "0.2.0"
$commit = (git rev-parse --short HEAD 2>$null) ?? "unknown"
$date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
New-Item -ItemType Directory -Force dist | Out-Null

foreach ($t in @("linux/amd64","linux/arm64","darwin/amd64","darwin/arm64","windows/amd64","windows/arm64")) {
    $os, $arch = $t.Split("/")
    $env:GOOS = $os; $env:GOARCH = $arch
    $suffix = if ($os -eq "windows") { ".exe" } else { "" }
    $name = "vaultkey_${version}_${os}_${arch}${suffix}"
    go build -trimpath -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$date" -o "dist\$name" .\cmd\vaultkey
}
```

### 11.3 Checksums

```powershell
Get-ChildItem dist\vaultkey_* | Get-FileHash -Algorithm SHA256 |
  ForEach-Object { "{0}  {1}" -f $_.Hash.ToLower(), (Split-Path $_.Path -Leaf) } |
  Set-Content dist\SHA256SUMS -Encoding ascii
```

### 11.4 DLL note

The current pipeline is `CGO_ENABLED=0` and can cross-build console binaries. A Windows DLL using Go `-buildmode=c-shared` requires:

- cgo enabled,
- a Windows C toolchain (mingw-w64 or MSVC-compatible),
- a build performed on/for Windows,
- exported `//export` functions with C-compatible types,
- careful string ownership and callback/thread rules.

Do not add a DLL build to the `0.2.0` release unless the human explicitly asks and the toolchain is available. The session process is the supported integration path now; the DLL can wrap `pkg/session.Engine` later.

---

## 12. Definition of done

- [ ] `pkg/vaultkey` and `pkg/session` exist with the documented public API.
- [ ] `internal/vaultkey` no longer exists (or only as a thin compatibility shim if the human asks).
- [ ] Windows idle-read regression test passes.
- [ ] Windows serial hardening + DCB/normalization tests pass.
- [ ] Windows VID-filtered discovery + fallback is implemented.
- [ ] Natural sort/dedupe is used on all platforms.
- [ ] `vaultkey session` implements protocol v1 and passes integration tests.
- [ ] Session keeps one port open across commands and preserves authorization.
- [ ] No mutating command is retried on ambiguity.
- [ ] Secrets never appear in logs/errors/argv.
- [ ] `--json-errors`, `version --json`, `capabilities`, `doctor --json` work.
- [ ] Existing 55 CLI + 18 prompt checks still pass (Linux/WSL).
- [ ] New session integration checks pass.
- [ ] `go test ./...`, `go test -race ./...` (where supported), `go vet ./...` pass.
- [ ] Windows amd64/arm64 build and vet pass.
- [ ] Six release artifacts build at 0.2.0; `SHA256SUMS` verifies.
- [ ] `docs/UI_INTEGRATION.md`, README, TEST_REPORT updated.
- [ ] `test/windows/smoke.ps1` and checklist added.
- [ ] Hardware checklist completed or explicitly marked blocked.
- [ ] No firmware/PCB/enclosure changes; no new dependencies.
- [ ] Git working tree clean; commits are coherent.

---

## 13. Do not do these

- Do not close and reopen the port between UI commands.
- Do not auto-reconnect without an explicit `reconnect` (or a deliberate, documented policy).
- Do not toggle DTR/RTS or use `DTR_CONTROL_HANDSHAKE`.
- Do not rely on Windows `ReadFile` returning only on data; handle `(0, nil)`.
- Do not use `bufio` without the zero-read wrapper for this port.
- Do not set `SetConsoleOutputCP` globally.
- Do not add `--password`/`--uri` flags.
- Do not log request bodies or secrets.
- Do not retry mutating commands.
- Do not add third-party libraries.
- Do not modify firmware to add a read-back command or expose the authorization deadline.
- Do not assume COM ports are shareable.
- Do not commit `bin/`, `dist/`, or test binaries.
- Do not claim Windows/hardware validation without running it.
- Do not use Wine.

---

## 14. Open questions for the human

Resolve these before or during Phase 0; they affect verification, not the protocol design.

1. **Repo location on Windows.** Where is the project copied? Is it a Git clone or a folder copy?
2. **Hardware availability.** Is a rev-D VaultKey unit available on the Windows machine? If yes, run the full checklist. If not, mark hardware blocked.
3. **UI stack.** C#/WinUI/WPF/WinForms? The session protocol is UI-agnostic, but the examples can be tailored.
4. **DLL timeline.** Is the DLL expected in this pass, or later? If now, is a C toolchain (mingw-w64/MSVC) installed, and which architecture (amd64, arm64)?
5. **Code signing / installer.** Is signing required for distribution? Not part of the code work, but affects release docs.
6. **Version.** Confirm 0.2.0 for the public API/session release.
7. **Git.** Should the Windows agent initialize Git and commit, or only produce a working tree?

---

## Appendix A — Current CLI reference (v0.1.0)

Global options:

| Option | Default | Notes |
|---|---|---|
| `--port PATH` | auto-detect | Also `VAULTKEY_PORT`. |
| `--timeout DUR` | `5s` | 500ms–1m. Also `VAULTKEY_TIMEOUT`. |
| `--debug` | off | Command names/sizes/status only. |
| `--version` | | Print version and exit. |
| `-h`, `--help` | | Help. |

Commands:

| Command | Auth required | Notes |
|---|---|---|
| `devices [--json]` | no | Probes candidate ports. |
| `info [--json]` | no | Product/firmware/hardware/protocol/port. |
| `status [--json]` | no | Lock/RTC/storage/startup/display/USB/provisioning/counts. |
| `doctor` | no | Read-only checks. |
| `time get [--json]` | no | UTC clock. |
| `time set [--unix N \| --rfc3339 T] [--json]` | yes | Defaults to now. |
| `list [--query TEXT] [--offset N] [--limit 1-25] [--all] [--json]` | yes | Labels only. |
| `add-credential --service TEXT [--username ...] [--url ...] [--notes ...] [--password-stdin] [--json]` | yes | Password prompted twice unless stdin. |
| `update-credential ID --service TEXT [options]` | yes | Full replacement. |
| `add-totp [--uri-stdin \| --uri-file PATH] [--json]` | yes | URI prompted unless stdin/file. |
| `delete --type credential\|totp --id N [--yes] [--json]` | yes | Confirms unless `--yes`. |
| `lock [--json]` | no | Revokes authorization. |
| `watch [--interval DUR] [--json]` | no | 250ms–1m; JSON Lines. |
| `version` | no | Human output. |

New in 0.2.0:

`session`, `capabilities`, `version --json`, `doctor --json`, global `--json-errors`.

---

## Appendix B — Protocol/limit reference

Commands from `VaultKey_Firmware/docs/USB_PROTOCOL.md`:

`GET_STATUS`, `GET_TIME`, `SET_TIME`, `LIST_SERVICES`, `ADD_CREDENTIAL`, `UPDATE_CREDENTIAL`, `ADD_TOTP`, `DELETE_ENTRY`, `LOCK_DEVICE`, `HELLO`, `GET_VERSION`.

Envelope:

```json
{"v":1,"id":"1","cmd":"...","ok":true,...}
```

Responses must echo the request ID. The client fails closed on mismatched IDs, malformed JSON, or missing `ok`.

Firmware behavior:

- Entry IDs are random non-zero 32-bit values.
- `LIST_SERVICES` omits usernames/URLs/notes/secrets.
- `ADD_TOTP` may return `detail: "invalid_secret"`; surface it in the hint.
- `LOCK_DEVICE` is accepted while locked.
- `GET_TIME` fails with `rtc_time_invalid` when the RTC is not valid.
- Credential update is a full replacement.
- TOTP support: SHA1/SHA256/SHA512, digits 6/8, period 15–300s, Base32 secret >= 10 bytes, issuer <= 64, account <= 96.
- HID typing supports bytes `0x20`–`0x7e`, tab, newline, backspace only; warn on other stored bytes.

---

## Appendix C — Session client examples

### C.1 PowerShell

```powershell
$exe = "C:\path\to\vaultkey.exe"

$psi = [System.Diagnostics.ProcessStartInfo]::new()
$psi.FileName = $exe
$psi.Arguments = "session --port COM5"
$psi.UseShellExecute = $false
$psi.CreateNoWindow = $true
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.StandardOutputEncoding = [System.Text.UTF8Encoding]::new($false)
$psi.StandardErrorEncoding = [System.Text.UTF8Encoding]::new($false)

$p = [System.Diagnostics.Process]::Start($psi)

# Startup event
$ready = $p.StandardOutput.ReadLine() | ConvertFrom-Json
if ($ready.type -ne "event" -or $ready.event -ne "ready") {
    throw "unexpected startup: $ready"
}

function Send-Request {
    param([int]$Id, [string]$Method, [hashtable]$Params = @{})
    $req = @{ id = $Id; method = $Method; params = $Params } | ConvertTo-Json -Compress
    $p.StandardInput.WriteLine($req)
    $p.StandardInput.Flush()
    while ($true) {
        $line = $p.StandardOutput.ReadLine()
        if ($null -eq $line) { throw "session ended" }
        $msg = $line | ConvertFrom-Json
        if ($msg.type -eq "response" -and $msg.id -eq $Id) { return $msg }
        # emit/handle events here
        Write-Host "event: $line"
    }
}

$caps = Send-Request -Id 1 -Method "capabilities"
$status = Send-Request -Id 2 -Method "status"
$p.StandardInput.WriteLine('{"id":3,"method":"shutdown"}')
$p.StandardInput.Flush()
$p.StandardInput.Close()
$p.WaitForExit()
```

Production UI notes:

- Read stdout asynchronously; the loop above is synchronous and can deadlock if events fill the pipe while you are not reading.
- Serialize requests; one in-flight.
- Match response IDs.
- On `ambiguous:true`, stop and verify; do not retry.

### C.2 C# (WinUI/WPF/WinForms)

```csharp
using System.Diagnostics;
using System.Text;
using System.Text.Json;

var psi = new ProcessStartInfo
{
    FileName = @"C:\path\to\vaultkey.exe",
    Arguments = "session --port COM5",
    UseShellExecute = false,
    CreateNoWindow = true,
    RedirectStandardInput = true,
    RedirectStandardOutput = true,
    RedirectStandardError = true,
    StandardOutputEncoding = new UTF8Encoding(false),
    StandardErrorEncoding = new UTF8Encoding(false),
};

using var process = Process.Start(psi)!;
var stdin = process.StandardInput;
var stdout = process.StandardOutput;

async Task<JsonDocument> SendAsync(int id, string method, object? parameters = null)
{
    var request = JsonSerializer.Serialize(new { id, method, @params = parameters });
    await stdin.WriteLineAsync(request);
    await stdin.FlushAsync();

    while (true)
    {
        var line = await stdout.ReadLineAsync();
        if (line is null) throw new IOException("session ended");
        var doc = JsonDocument.Parse(line);
        var root = doc.RootElement;

        if (root.TryGetProperty("type", out var type) &&
            type.GetString() == "response" &&
            root.GetProperty("id").GetInt32() == id)
        {
            return doc;
        }
        // Handle events (status/watch_error/disconnected) here.
    }
}
```

Use a `Channel<string>`/background reader task so watch events do not starve responses. Never block the UI thread. Dispose/kill the child on app exit; prefer a Job Object.

---

## Appendix D — Manual hardware checklist

Copy this into `test/windows/README.md` and fill it in.

```text
Date:
Windows version:
Architecture:
VaultKey firmware:
Port:
Go version:

[ ] Device Manager shows the COM port
[ ] devices --json lists it
[ ] info: product VaultKey, protocol 1
[ ] status while locked: counts hidden
[ ] session starts; ready event has the port/device
[ ] unlock on device + Authorize CDC 5 min; status provisioning=true
[ ] idle 60s; status still authorized
[ ] watch --interval 30s for 2 minutes; no disconnect
[ ] list
[ ] add credential (ASCII)
[ ] update credential
[ ] delete credential
[ ] add TOTP
[ ] delete TOTP
[ ] lock; status locked/provisioning=false
[ ] management while locked: exit 4
[ ] management unauthorized: exit 5
[ ] re-authorize while session open; management works
[ ] wait for 5-minute expiry; provisioning=false
[ ] close session; device shows not authorized
[ ] unplug/replug; disconnected event; reconnect
[ ] port busy with another terminal; port_busy error
[ ] COM10+ port opens
[ ] non-ASCII credential triggers HID warning
[ ] ambiguous mutating timeout -> exit 7 / ambiguous:true; no retry
[ ] HID typing works for ASCII
[ ] secrets absent from --debug/session logs/argv
[ ] 128 credential + 128 TOTP boundary (if feasible)
```

---

## Appendix E — Firmware evidence

Use these when reviewing the authorization/session design. Do not modify the firmware.

```text
VaultKey_Firmware/src/core/Application.cpp
  142-144  revokeProvisioning() when the window exists but is no longer authorized
  272-276  provisioningAuthorized() = unlocked && cdcConnected && deadline
  312-329  GET_STATUS fields

VaultKey_Firmware/src/hardware/USBComposite.cpp
  66-68    cdcConnected() = driverStarted && bool(cdc_)

Arduino core USBCDC.cpp
  469-474  operator bool() returns `connected`
  216-275  _onLineState: connected=true only for DTR && RTS; false when DTR drops;
           esptool-style DTR/RTS sequence can restart to bootloader
```

Protocol/limits:

```text
VaultKey_Firmware/docs/USB_PROTOCOL.md
VaultKey_Firmware/include/core/HardwareConfig.h
```

The firmware's `provisioningSecondsRemaining()` exists internally but is not exposed in protocol v1. Do not add it to the protocol in this pass.

---

## Appendix F — Suggested commit sequence

```text
1. chore: move vaultkey package to pkg/vaultkey
2. feat: add public Open/Discover/Session and ErrorInfo
3. fix(windows): tolerate idle zero-byte serial reads
4. feat(windows): harden serial errors, timeouts, and port naming
5. feat(windows): filter COM ports by VID 303A and sort naturally
6. feat: add public session JSON Lines engine
7. feat(cli): add session command
8. feat(cli): add --json-errors, capabilities, version --json, doctor --json
9. test: session, error, and Windows regression coverage
10. docs: UI integration and Windows handoff
11. release: 0.2.0
```

Run `go test ./...` and `go vet ./...` before each commit. Do not mix the package move with behavioral changes; keep the move reviewable.

---

## Final note to the agent

The existing code is deliberately conservative: one request at a time, no mutating retries, strict IDs/limits, no secrets in logs. Preserve those properties. The session is an additive layer, not a replacement for the protocol client. When in doubt, prefer a clear error over a clever retry.

If hardware is unavailable on the Windows machine, still complete the code, unit tests, Windows builds, docs, and the manual checklist template. Report exactly what was and was not executed.
