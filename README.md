# VaultKey Companion

`vaultkey` is a dependency-free command-line companion for the VaultKey
rev-D USB security key. It talks to the device over its CDC-ACM serial
interface using the firmware's JSON Lines protocol v1. It does not modify the
firmware, PCB, or enclosure projects.

The companion is intentionally read-through-the-device: it can add, replace,
list labels, import TOTP URIs, set the clock, and lock the vault, but it cannot
read passwords back, unlock the vault, or bypass the physical PIN.

> **Hardware status:** this version was validated end-to-end against a
> protocol-faithful PTY simulator because no physical rev-D board was available
> during development. The simulator is test-only; the shipped binary contains
> no fake-device code. See [docs/TEST_REPORT.md](docs/TEST_REPORT.md).

## Contents

- [Features](#features)
- [Security model](#security-model)
- [Requirements](#requirements)
- [Build and install](#build-and-install)
- [Authorization workflow](#authorization-workflow)
- [Commands](#commands)
- [Input handling](#input-handling)
- [Exit codes](#exit-codes)
- [Device limits](#device-limits)
- [OS permissions](#os-permissions)
- [Troubleshooting](#troubleshooting)
- [Development and testing](#development-and-testing)
- [Repository layout](#repository-layout)
- [Limitations](#limitations)

## Features

- Auto-detects VaultKey CDC ports on Linux, macOS, and Windows, or accepts an
  explicit `--port`.
- Read-only inspection: identity, lock/RTC/storage/USB state, clock, and a
  diagnostic report.
- Credential management: add, full replacement by entry ID, list labels,
  delete with confirmation.
- TOTP import from an `otpauth://totp/` URI.
- Time management with validation against the DS1307 calendar range.
- `lock` clears the vault lock state and revokes CDC authorization.
- `watch` polls status until interrupted.
- JSON output for scripts (`--json`; `watch --json` emits JSON Lines).
- No third-party Go modules; the CLI is cgo-free. The optional Windows DLL
  prototype in `cmd/vaultkey-dll` uses cgo and is not part of the release.

## Security model

### What the app can do

- Send only the documented protocol v1 commands:
  `HELLO`, `GET_VERSION`, `GET_STATUS`, `GET_TIME`, `SET_TIME`,
  `LIST_SERVICES`, `ADD_CREDENTIAL`, `UPDATE_CREDENTIAL`, `ADD_TOTP`,
  `DELETE_ENTRY`, and `LOCK_DEVICE`.
- Add secrets to the device; the firmware encrypts stored records with
  AES-GCM under its PIN-derived master key.
- Read non-secret labels (service, TOTP issuer/account) and device status.
- Lock the vault and revoke the five-minute CDC authorization.

### What the app cannot do

- Unlock the device. PIN entry happens only on the physical keypad.
- Authorize CDC itself. The user must choose **USB Functions -> Authorize CDC
  5 min** on the device.
- Read passwords, TOTP secrets, or TOTP codes back over CDC.
- Change the PIN or factory-reset the device.
- Type secrets over HID. `add-credential` stores them; the device's HID
  keyboard path types them on request from the device UI. The firmware can
  type ASCII (`0x20`-`0x7e`, tab, newline, backspace) only; the app warns when
  a username or password contains other bytes.

### Secret handling

- There is deliberately **no `--password` or `--uri` argument**, so secrets do
  not appear in shell history or process listings.
- Interactive input uses a hidden prompt on the controlling terminal, disables
  echo, and restores the terminal on completion, error, or Ctrl-C.
- Non-interactive input is read from stdin (`--password-stdin`,
  `--uri-stdin`) or, for TOTP only, `--uri-file`.
- Passwords are prompted twice on add/update; a mismatch exits before any
  device connection is made.
- `--debug` logs command names, byte counts, and success/failure only. Request
  fields and response bodies are never logged. Byte counts still reveal secret
  lengths, so avoid `--debug` when that matters.
- Secrets are sent only to the device. The app does not write them to disk,
  logs, clipboard, or argv.
- Go strings are immutable, so the app cannot guarantee memory zeroization.
  A compromised host can observe the process; the device protects data at
  rest, not against a hostile host.

### Mutating commands are never retried

`SET_TIME`, `ADD_CREDENTIAL`, `UPDATE_CREDENTIAL`, `ADD_TOTP`,
`DELETE_ENTRY`, and `LOCK_DEVICE` are treated as mutating. If the request was
written but the response was lost, the app exits with code `7` and says the
outcome is unknown. It does **not** retry. Reconnect and inspect state
(`status`, `list`, `time get`) before deciding what to do.

## Requirements

- Go 1.24 or newer to build. No network access is needed after checkout; the
  project has no external modules.
- The device must run firmware protocol v1. The companion rejects any other
  protocol value before issuing management commands.
- Linux, macOS, or Windows. Linux and macOS use termios directly; Windows uses
  the `kernel32` DCB/COMMTIMEOUTS API.
- For interactive password/TOTP entry: a controlling terminal. Otherwise use
  the stdin options.

## Build and install

```sh
make build                 # bin/vaultkey with version/commit/date ldflags
./bin/vaultkey version
```

Direct build:

```sh
CGO_ENABLED=0 go build -trimpath -o bin/vaultkey ./cmd/vaultkey
```

Release artifacts for Linux, macOS, and Windows (amd64 and arm64) plus
`SHA256SUMS`:

```sh
make release               # writes to dist/
```

### Optional Windows DLL prototype

`cmd/vaultkey-dll` is an experimental x64 DLL that wraps the same protocol
client behind one JSON-in/JSON-out C entry point. It needs cgo plus a
mingw-w64 toolchain and is deliberately kept out of `make release`:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
go build -buildmode=c-shared -trimpath \
  -ldflags '-s -w -X main.version=0.1.0 -X main.commit=prototype -X main.buildDate=2026-11-17T00:00:00Z' \
  -o dist/vaultkey_windows_amd64.dll ./cmd/vaultkey-dll
```

See [docs/DLL_BUILD_NOTES.md](docs/DLL_BUILD_NOTES.md) for the build failures,
fixes, known gaps, and the reproduction script.

The same six targets cross-compile from any host:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/vaultkey-linux-arm64 ./cmd/vaultkey
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /tmp/vaultkey.exe ./cmd/vaultkey
```

Install by copying `bin/vaultkey` (or a release binary) somewhere on `PATH`.

## Authorization workflow

Management commands need all three of the following; read-only commands do
not:

1. **Unlock the vault** with the device PIN.
2. **Keep the CDC port open.** Authorization is tied to the active CDC
   connection.
3. On the device, open **USB Functions -> Authorize CDC 5 min** and activate
   it.

The authorization window is about five minutes. Repeated commands do **not**
extend it; re-authorize when it expires. `LOCK_DEVICE` revokes it immediately.
Use `vaultkey doctor` to see which condition is missing.

```sh
# 1. Connect and confirm the device answers.
vaultkey devices
vaultkey info

# 2. Unlock with the PIN on the device, then authorize CDC on the device.
vaultkey status
vaultkey doctor

# 3. Manage entries within the authorization window.
vaultkey list
```

If `status` reports `CDC auth: no`, the entries line stays hidden and
management commands exit with code `5`.

## Commands

Global options may appear before or after the command:

| Option | Default | Meaning |
|---|---|---|
| `--port PATH` | auto-detect | Serial port. Also `VAULTKEY_PORT`. |
| `--timeout DUR` | `5s` | Per-request timeout, `500ms`-`1m`. Also `VAULTKEY_TIMEOUT`. |
| `--debug` | off | Log command names, sizes, and status to stderr. |
| `--version` | | Print the app version and exit. |
| `-h`, `--help` | | Show help. |

| Command | Purpose | Needs unlock + CDC auth |
|---|---|---|
| `devices [--json]` | Probe candidate ports for a VaultKey. | no |
| `info [--json]` | Product, firmware, hardware, protocol, port. | no |
| `status [--json]` | Lock, RTC, storage, startup, display, USB, auth, counts. | no |
| `doctor` | Read-only connection and authorization checks. | no |
| `time get [--json]` | Read the device's UTC clock. | no |
| `time set [--unix N \| --rfc3339 T] [--json]` | Write UTC time (default: now). | yes |
| `list [--query TEXT] [--offset N] [--limit 1-25] [--all] [--json]` | List labels and paginate. | yes |
| `add-credential --service TEXT [options]` | Add a credential. Password prompted twice. | yes |
| `update-credential ID --service TEXT [options]` | Replace all fields of a credential. | yes |
| `add-totp [--uri-stdin \| --uri-file PATH] [--json]` | Import a TOTP URI. | yes |
| `delete --type credential\|totp --id N [--yes]` | Delete an entry after confirmation. | yes |
| `lock [--json]` | Lock the vault and revoke CDC authorization. | no |
| `watch [--interval DUR] [--json]` | Poll status until interrupted. | no |

### Examples

```sh
# Discovery and inspection
vaultkey devices
vaultkey devices --json
vaultkey info
vaultkey status
vaultkey doctor

# Clock (UTC; DS1307 range 2000-2099)
vaultkey time get
vaultkey time set
vaultkey time set --unix 1794528000
vaultkey time set --rfc3339 2026-11-14T22:00:00Z --json

# Labels only: never usernames, passwords, notes, or TOTP secrets
vaultkey list
vaultkey list --query github
vaultkey list --all --json
vaultkey list --offset 25 --limit 10

# Add a credential; password is prompted twice with echo disabled
vaultkey add-credential \
  --service "GitHub" \
  --username "octocat" \
  --url "https://github.com" \
  --notes "work account"

# Scripted password input (no argv exposure)
vaultkey add-credential --service "Deploy" --password-stdin < /run/secrets/deploy

# Complete replacement; omitted username/url/notes are cleared
vaultkey update-credential 1234567890 \
  --service "GitHub" --username "octocat" --password-stdin < /run/secrets/github

# TOTP import
vaultkey add-totp --uri-file /run/secrets/totp-uri
vaultkey add-totp --uri-stdin < /run/secrets/totp-uri

# Delete (prompts unless --yes)
vaultkey delete --type credential --id 1234567890
vaultkey delete --type totp --id 987654321 --yes

# Lock and revoke authorization
vaultkey lock --json

# Observe state until Ctrl-C (exit 130)
vaultkey watch --interval 2s
vaultkey watch --json --interval 5s
```

`--all` follows pagination in 25-entry pages and rejects combining with
`--offset`. Listing uses the firmware's case-insensitive substring match on
labels; queries are limited to 64 bytes.

## Input handling

- `add-credential` / `update-credential`: interactive hidden prompt, twice.
  `--password-stdin` reads exactly one line from stdin. One trailing CR or LF
  is removed and does not count against the field limit; empty passwords are
  rejected by the device and by local validation. Non-secret fields are
  validated before the password is read.
- `add-totp`: hidden prompt by default; `--uri-stdin` reads one line;
  `--uri-file` reads the first line of a file. The URI must start with
  `otpauth://totp/` and be at most 1024 bytes. The device supports SHA1,
  SHA256, SHA512, 6 or 8 digits, and periods of 15-300 seconds, with a Base32
  secret of at least 10 bytes.
- `delete` without `--yes` asks for confirmation on the controlling terminal
  and shows the label when available. `--yes` is intended for scripts.
- If no terminal is available, hidden prompts fail with exit code `2` and a
  hint to use the stdin option.

## Exit codes

| Code | Meaning |
|---:|---|
| `0` | Success. |
| `1` | General failure. |
| `2` | Usage, local validation, prompt, or input error. |
| `3` | Connection, serial port, permission, or device discovery error. |
| `4` | The device is locked. |
| `5` | CDC provisioning is not authorized on the device. |
| `6` | Protocol error (malformed, mismatched, or unsupported response). |
| `7` | Timeout, or an ambiguous mutating command whose outcome is unknown. |
| `8` | Device operation failed (storage, delete, RTC, time verification). |
| `130` | Interrupted with Ctrl-C / SIGINT. |

## Device limits

Mirrors `include/core/HardwareConfig.h` in the firmware repository.

| Item | Limit |
|---|---:|
| Service | 64 bytes |
| Username | 128 bytes |
| Password | 256 bytes (required) |
| URL | 192 bytes |
| Notes | 512 bytes |
| TOTP URI | 1024 bytes |
| TOTP issuer / account | 64 / 96 bytes |
| LIST query | 64 bytes |
| Page size | 1-25 (default 20; `--all` uses 25) |
| Page offset | 0-256 |
| Credentials | 128 |
| TOTP entries | 128 |
| Request line | 2048 bytes before LF |

Entry IDs are random non-zero 32-bit values chosen by the firmware, not
sequential numbers. Use `list` to discover them.

## OS permissions

### Linux

- The device appears as `/dev/ttyACM0` (native USB CDC).
- Your user needs read/write access. Common groups are `uucp` (Arch) or
  `dialout` (Debian/Ubuntu/Fedora):
  `sudo usermod -aG uucp "$USER"` (log out and back in).
- Optional udev rule for the prototype VID/PID:
  `SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", MODE="0660", GROUP="uucp"`.
- Port discovery checks `/dev/ttyACM*` and `/dev/ttyUSB*`, using sysfs vendor
  metadata to skip clearly unrelated adapters when available. Pass `--port` to
  bypass discovery.
- `devices` opens each candidate and sends `HELLO`, so it can briefly touch
  unrelated serial devices if sysfs metadata is unavailable.

### macOS

- Use `/dev/cu.usbmodem*` (or `/dev/cu.usbserial*`). Avoid `/dev/tty.*`, which
  waits for carrier detection.
- No special group is normally required for the built-in CDC driver.

### Windows

- The port appears as `COM5` (or similar) once the CDC driver binds. Windows
  normally uses the in-box `usbser.sys`; no custom driver or INF is included.
- Discovery reads `HKLM\HARDWARE\DEVICEMAP\SERIALCOMM`. If the port is
  missing, check Device Manager and pass `--port COM5` explicitly.
- Hidden prompts read `CONIN$`; run from a console window, not a detached
  process.

## Troubleshooting

| Symptom | Meaning and fix |
|---|---|
| `the device is locked` (exit 4) | Enter the PIN on the device, then retry. |
| `CDC provisioning is not authorized` (exit 5) | On the device: **USB Functions -> Authorize CDC 5 min**. Keep the port open. |
| `no candidate VaultKey serial ports` | Check cable/firmware, install permissions, or pass `--port`. |
| `permission denied` (exit 3) | Add your user to `uucp`/`dialout` on Linux. |
| `device did not respond before the timeout` (exit 7) | Wrong port, unresponsive firmware, or too-short `--timeout`. Read-only commands are safe to retry. |
| `outcome unknown` (exit 7) | The device may have applied a mutating command. Reconnect, run `status`, `list`, or `time get`, then decide. Do not blind-retry. |
| `protocol error` (exit 6) | The app and firmware protocol versions differ, or the response was malformed. Update one side so both use protocol v1. |
| `invalid_totp` | The URI was rejected. If the device supplied a detail, it appears in the hint; otherwise check secret, digits, period, and algorithm. |
| `non-ASCII/control bytes` warning | The device can store the value but its HID keyboard cannot type it. Use ASCII if HID output is required. |
| `RTC time is not valid` (exit 8) | Set the clock with `vaultkey time set` while unlocked and authorized. |
| `the entry was not found` / `delete_failed` (exit 8) | Refresh `list`; the ID or type may be wrong. |

## Development and testing

```sh
make check        # gofmt check, go vet, unit tests, race tests
make integration  # PTY simulator CLI + hidden-prompt checks (needs python3)
make release      # six cross-built artifacts with SHA256SUMS
```

- Unit tests cover protocol framing, correlation, error mapping, validation,
  pagination, and ambiguous-write classification.
- Integration checks run the real CLI against `test/integration/sim.py`, a
  test-only PTY simulator that implements protocol v1 state transitions. It
  contains no cryptography and no production code path depends on it.
- Cross-builds cover `linux/amd64`, `linux/arm64`, `darwin/amd64`,
  `darwin/arm64`, `windows/amd64`, and `windows/arm64`.

See [docs/TEST_REPORT.md](docs/TEST_REPORT.md) for results and the explicit
list of what was not tested.

## Repository layout

```text
cmd/vaultkey/            process entry point and ldflags version variables
cmd/vaultkey-dll/        optional Windows DLL prototype (cgo; not in release)
internal/cli/            argument parsing, commands, output, exit codes
internal/dllapi/         DLL error mapping shared with the cgo bridge
internal/vaultkey/       protocol client, typed API, validation, limits
internal/transport/      Linux/macOS termios, Windows DCB, port discovery
internal/prompt/         hidden terminal input and confirmation
test/integration/        PTY simulator and end-to-end checks (test-only)
ui/index.html            single-file sample UI (demo + WebView2 host bridge)
ui/README.md             sample UI modes, host contract, and limits
docs/TEST_REPORT.md      verification record and gaps
docs/WINDOWS_AGENT_HANDOFF.md  brief for the Windows UI/session implementation
docs/DLL_BUILD_NOTES.md  DLL prototype build failures and reproduction
scripts/build-release.sh cross-build helper
scripts/reproduce-dll-build.sh  reproduces the DLL build and its failures
```

## Limitations

- **Physical hardware was not available.** USB enumeration, CDC/HID composite
  behavior, real RTC/storage faults, and the on-device authorization UI were
  not exercised against a rev-D board.
- The app cannot display or export existing secrets, by design and by protocol.
- The five-minute authorization window is enforced by the device; the app does
  not extend it.
- Credential updates are full replacements. There is no partial field update.
- The app does not manage the PIN, factory reset, keyboard layout, contrast, or
  other device settings.
- No automatic retries for mutating commands. This is deliberate.
- Go cannot zeroize immutable strings; host memory is not a secure enclave.
- Windows and macOS binaries were cross-compiled but only Linux was executed in
  the development environment.
- The Windows DLL prototype was built and statically inspected, but not loaded
  on Windows and not tested against a rev-D board. Only amd64 is built; no
  arm64 mingw toolchain is available.
