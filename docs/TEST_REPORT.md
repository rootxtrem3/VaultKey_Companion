# Test report

Date: 2026-11-14

## Verdict

| Area | Result |
|---|---|
| `gofmt` | clean |
| `go vet ./...` | pass |
| Unit tests (`go test ./...`) | 30 functions, pass |
| Race detector (`go test -race ./...`) | pass |
| PTY CLI integration checks | 55 / 55 pass |
| PTY hidden-prompt checks | 18 / 18 pass |
| Cross-builds, 6 targets | pass, checksums verified |
| Release checksums | `SHA256SUMS` verifies |
| Physical rev-D hardware | **not tested (no board available)** |

The binary under test was built with:

```text
vaultkey 0.1.0 (commit unknown, built 2026-11-14T20:34:01Z)
go1.27.4 linux/amd64, CGO_ENABLED=0, -trimpath, stripped
```

`commit unknown` is expected: the companion directory is not a Git repository
yet.

## Environment

```text
OS:      Linux 7.2.15-arch1-1 x86_64
Go:      go1.27.4 linux/amd64
Serial:  no /dev/ttyACM* or /dev/ttyUSB* present
Device:  no physical VaultKey rev-D available
```

The firmware source and `docs/USB_PROTOCOL.md` were used as the protocol
reference. No firmware, PCB, or enclosure files were modified.

## Unit tests

### `internal/vaultkey` (21 functions)

Protocol:

- `HELLO` request shape (`v`, `id`, `cmd`) and identity decoding.
- Device error mapping (`ok:false`, error code, `detail`).
- Fragmented responses and blank-line tolerance.
- Mismatched request IDs, malformed JSON, and missing `ok` become
  `ProtocolError`.
- Oversized requests and reserved field names are rejected locally.
- Mutating timeout and mutating write failure produce `*AmbiguousError`.
- Non-mutating timeout and write failure are not ambiguous.
- Read timeout is reported as `context.DeadlineExceeded`.
- Close reports `ErrClosed` on later calls.
- Add-credential field transfer and `entry_id` validation.
- List page decoding.

Validation:

- Credential byte limits for service, username, password, URL, notes.
- Required fields and invalid UTF-8.
- TOTP URI prefix, label presence, and length limit.
- DS1307 UTC range 2000-2099.
- HID-typeable ASCII set (printable, tab, newline, backspace).

### `internal/cli` (7 functions)

- Global option parsing before/after the command, `--opt=value`, environment
  fallbacks, and the `--` terminator.
- Timeout bounds and malformed durations.
- One-line stdin reading, CRLF handling, and empty input. Exact-limit content
  is accepted with or without a trailing LF/CRLF; content one byte over the
  limit is rejected.
- Device error code to exit-code mapping.
- `--all` pagination: 30 entries over two pages, no duplicate IDs, totals
  preserved.

### `internal/prompt` (2 functions)

- `readLimitedLine` applies the same maximum-content semantics as stdin:
  exactly the limit in bytes is accepted with LF, CRLF, or no terminator, and
  one extra byte is rejected.
- Empty input reports `io.EOF` rather than an empty secret.

### Commands

```sh
make check
```

Result: exit 0. `go test -race` also reports no data races.

## Integration checks

Because no hardware was available, the CLI was run against
`test/integration/sim.py`, a test-only simulator that creates a real PTY pair
and implements protocol v1. It is not linked into the binary and has no
production code path. Unit and integration tests use the same request
framing, correlation, pagination, and error handling as a real device
connection.

Simulator fidelity:

- Random non-zero 32-bit entry IDs, as allocated by the firmware.
- `device_locked` and `provisioning_not_authorized` gating; entry counts are
  hidden while unauthorized.
- Status keys match `Application::handleUSBCommand`: `locked`, `rtc`,
  `storage`, `startup`, `display`, `button_event_drops`, `usb_driver`,
  `usb_mounted`, `cdc`, `hid`, `provisioning`, and conditional
  `credentials`/`totp`.
- `DELETE_ENTRY` returns `delete_failed` for unknown IDs.
- A configurable dropped response exercises the ambiguous-write path without
  blocking unrelated commands.
- Firmware request limits, page size, offset, query folding, and local
  validation are mirrored.

What the simulator does **not** model: USB enumeration, HID output, RTC
hardware, AES-GCM storage, power loss, or the device UI.

### CLI checks (`check_cli.sh`, 55 assertions)

| Group | Checks |
|---|---|
| Identity/discovery | text and JSON `info`, port/product/protocol assertions, device probing |
| Status/doctor | locked/unlocked/authorized states, entry counts hidden when unauthorized, doctor warnings |
| Clock | `time get`, `time set --unix`, `time set --rfc3339`, device echo and verification, out-of-range rejection |
| Listing | text/JSON pages, query filter, `--all` pagination, updated label visible |
| Credentials | stdin password, random `entry_id`, non-ASCII HID warning, full replacement |
| TOTP | valid import, invalid scheme rejected locally, delete by returned ID |
| Delete | credential and TOTP success, unknown ID exits 8, invalid type exits 2 |
| Lock/auth | lock JSON, status after lock, list exits 4, unauthorized management exits 5, lock allowed while unauthorized |
| Ambiguous write | dropped `LOCK_DEVICE` exits 7 with the "may have been applied" warning |
| Errors | unknown command, missing command, missing port, oversized service, empty stdin, bad type, bad time |
| Watch | JSON Lines emitted, SIGINT exits 130 |

Result:

```text
RESULT: 55 passed, 0 failed
```

### Hidden-prompt checks (`check_prompts.py`, 18 assertions)

Each case runs the real binary on a PTY with a controlling terminal.

- Add-credential with two prompts exits 0 and prints an entry ID.
- Terminal `ECHO` is off while the prompt is active and restored afterwards.
- The typed password is absent from the whole transcript, including `--debug`
  output.
- Mismatched confirmation exits 2 before connecting; both secrets stay
  unprinted; echo is restored.
- Invalid non-secret metadata (missing service) exits 2 before the password
  prompt, so no password is consumed.
- Ctrl-C at the password prompt exits 130 and restores echo.
- TOTP URI prompt hides the Base32 secret, adds the entry, and restores echo.

Result:

```text
RESULT: 18 passed, 0 failed
```

Reproduce with:

```sh
make integration
```

## Cross-builds

`make release` produced:

| Target | Format | Size |
|---|---|---:|
| linux/amd64 | ELF 64-bit x86-64, statically linked, stripped | 2.9 MB |
| linux/arm64 | ELF 64-bit ARM aarch64, statically linked, stripped | 2.8 MB |
| darwin/amd64 | Mach-O 64-bit x86_64, PIE | 2.9 MB |
| darwin/arm64 | Mach-O 64-bit arm64, PIE | 2.8 MB |
| windows/amd64 | PE32+ console, x86-64 | 3.0 MB |
| windows/arm64 | PE32+ console, ARM64 | 2.8 MB |

All six binaries are `CGO_ENABLED=0`, built with `-trimpath` and
`-ldflags '-s -w'`. `sha256sum -c SHA256SUMS` passes for all six. Only the
Linux amd64 binary was executed; the others were format-verified, not run.

## Security checks

- `go list -m all` shows only the module itself: no third-party dependencies.
- No `--password` or `--uri` flag exists; secrets enter via hidden prompt or
  stdin/file. `grep` over the flag definitions confirms only
  `--password-stdin`, `--uri-stdin`, and `--uri-file`.
- Debug logging records command names, byte counts, and success only. The
  prompt transcript test confirms no secret appears in `--debug` output.
- Mutating write failures and timeouts are classified as ambiguous; tests
  cover both. The CLI prints a warning and never retries.
- Terminal echo is restored on success, mismatch, and SIGINT; verified with
  `tcgetattr` on the PTY slave.
- The request limit is enforced locally before writing; the device also
  enforces 2048 bytes.
- Response IDs must match the request; mismatches and malformed responses fail
  closed with exit 6.
- Error messages contain field names and limits, not field values. Device
  `detail` text is only used for TOTP parse hints.
- The shipped code contains no simulator or fake-device references.

## Not tested

These remain open until a physical rev-D unit is available:

1. USB composite enumeration (CDC + HID) on Linux, macOS, and Windows.
2. Real CDC open/close, line discipline, and unplug/replug behavior.
3. The on-device PIN unlock and **USB Functions -> Authorize CDC 5 min**
   workflow, including the five-minute expiry.
4. Firmware AES-GCM storage, PIN envelope, RTC, LittleFS, and fault paths.
5. Actual HID typing of stored ASCII credentials and the non-ASCII warning.
6. Full 128-credential + 128-TOTP pagination against firmware storage.
7. Windows runtime behavior: DCB configuration, SERIALCOMM enumeration,
   `CONIN$` prompts, and Ctrl-C handling.
8. macOS runtime behavior: termios and `/dev/cu.usbmodem*` discovery.
9. Permission failures (`uucp`/`dialout`), udev rules, and driver binding.
10. Long-run `watch` stability and device disconnect during a mutating
    command.
11. Fuzzing of the response parser and TOTP URI parser beyond the unit cases.

The simulator cannot substitute for these. Treat hardware validation as the
next milestone before distributing binaries.
