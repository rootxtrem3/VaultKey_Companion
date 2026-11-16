# Integration checks (test-only)

These scripts exercise the real CLI against a PTY simulator that implements
the firmware's JSON Lines protocol v1. They are not part of the production
binary, do not touch hardware, and contain no cryptography.

> **Warning:** the checks add and delete fake entries and modify the fake
> clock. Do not point `PORT` at a real VaultKey.

## Run

From the repository root:

```sh
./test/integration/run.sh
# or
make integration
```

Requirements: `bash`, `python3` (3.8+), and Go. `run.sh` builds a temporary
binary, starts a fresh simulator, runs both check suites, and cleans up.

Expected result: 55 CLI checks and 18 hidden-prompt checks, all passing.

## Files

| File | Purpose |
|---|---|
| `sim.py` | Creates a real PTY pair, links it as `$VK_WORK/tty`, and speaks protocol v1. State lives in `$VK_WORK/state.json`. |
| `check_cli.sh` | Exercises discovery, info/status/doctor, clock, list/pagination, add/update/TOTP/delete, lock, authorization errors, ambiguous timeout, usage errors, and watch. |
| `check_prompts.py` | Drives hidden prompts through a PTY and checks echo state, mismatch handling, Ctrl-C cleanup, and transcript leakage. |
| `run.sh` | Orchestrates a clean run. |

## Simulator state

```json
{"locked": false, "authorized": true, "drop": ""}
```

- `locked`: management commands return `device_locked`; only locked-safe
  commands succeed.
- `authorized`: management commands return `provisioning_not_authorized` when
  false.
- `drop`: name of a command whose response is silently discarded, to exercise
  the ambiguous-write path. Other commands keep working.

The simulator allocates random non-zero 32-bit entry IDs like the firmware,
returns `delete_failed` for unknown IDs, hides entry counts while
unauthorized, and uses the firmware's error codes. It is a protocol test
double, not a model of the hardware, storage encryption, HID, RTC, or USB
stack.

## Environment overrides

- `VK`: CLI binary to test (default: `bin/vaultkey`).
- `VK_WORK`: scratch directory (default: `${TMPDIR:-/tmp}/vaultkey-it`).
- `PORT`: PTY link path (default: `$VK_WORK/tty`).
- `VK_LINK`, `VK_STATE`, `VK_LOG`: simulator paths.

`check_cli.sh` and `check_prompts.py` can be run separately if a simulator is
already listening and has a fresh state.
