# Sample companion UI

`index.html` is a single-file, dependency-free sample of the VaultKey Companion
UI. It is a design and integration reference, not a production front end.

Open it directly in a browser for a fully interactive demo:

- Windows: `start ui\index.html`
- Linux: `xdg-open ui/index.html`
- macOS: `open ui/index.html`

No server, build step, external font, or network access is required.

## Modes

### Demo mode

When the page is opened directly, it creates a simulated device and connects
automatically. The demo backend mirrors the session protocol shapes:

- seeded credentials and TOTP entries
- continuous 5-minute CDC authorization countdown
- lock and authorization-expiry simulation
- unplug/reconnect that revokes authorization like a dropped DTR line
- add, replace, delete, and TOTP import flows
- client-side paging and search
- an activity log that records method names and error codes only

The demo backend stores only the labels the firmware exposes. Passwords and
TOTP secrets exist in the form fields and are not written to the activity log
or to any persistent storage.

### WebView2 host mode

If `window.chrome.webview` exists, the page assumes a native host owns a
long-lived `vaultkey session` process.

The page posts the exact session request object:

```json
{"id":1,"method":"status","params":{},"timeout_ms":5000}
```

The host writes that object as one JSON line to the session's stdin and
forwards every stdout line back verbatim:

```json
{"type":"response","id":1,"ok":true,"result":{}}
{"type":"response","id":2,"ok":false,"error":{"kind":"locked","code":"device_locked","message":"the device is locked","hint":"unlock the device with its PIN, then retry.","exit_code":4,"ambiguous":false,"device_code":"device_locked"}}
{"type":"event","event":"ready","result":{"session_protocol":1,"port":"COM5","device":{"product":"VaultKey","firmware":"1.0.0","hardware":"rev-D","protocol":1}}}
{"type":"event","event":"status","result":{"time":"2026-11-16T12:00:00Z","status":{}}}
{"type":"event","event":"disconnected","error":{}}
{"type":"event","event":"fatal","error":{}}
```

Host requirements:

- Start one session process and keep it open. Windows COM ports are exclusive,
  and closing the port drops DTR, which revokes the device's CDC authorization.
- Keep requests serial and preserve request IDs. The session already enforces
  one in-flight request; the host must not add parallelism.
- Replay `ready` to a page that attaches after the session started.
- Forward `status`, `watch_error`, `disconnected`, and `fatal` events without
  rewriting them.
- Keep stderr for diagnostics only; stdout carries protocol JSON.
- Never log request bodies. Passwords and TOTP URIs are accepted only as JSON
  parameters, never through process arguments.

Process lifecycle is host-owned, not part of session protocol v1. The page
asks the host to stop with:

```json
{"kind":"vaultkey.control","action":"disconnect"}
```

The host decides whether to stop the process or just close the port.

## Session methods used

Read-only: `capabilities`, `hello`, `status`, `time.get`, `list`.

Mutating: `credentials.add`, `credentials.update`, `totp.add`, `delete`,
`lock`.

Connection: `reconnect`.

`watch.start`/`watch.stop` and `doctor` are intentionally not exposed by this
sample. A status event is still handled if the host starts a watch.

The "All" tab calls `list` repeatedly with `limit: 25` and pages locally so
credentials and TOTP share one row sequence. The single-type tabs use device
paging with `offset` and `limit`.

## Error and secret handling

- Failures display `message`, `code`, and `hint` from the session error object.
- Mutating requests are never retried automatically.
- A timeout is shown as a timeout; the UI does not assume the write failed.
- The firmware exposes only credential labels and TOTP issuer/account. Replace
  is a full overwrite and starts with blank fields.
- Secrets are held in form fields and request objects only. They are not put
  in URLs, logs, activity entries, or `localStorage`.

## Limits

`LIMITS` mirrors `internal/vaultkey/types.go`: service 64, username 128,
password 256, URL 192, notes 512, query 64, TOTP URI 1024, request line 2048,
page limit 25, page offset 256, and 128 credentials plus 128 TOTP entries.
The device remains the authority; the UI validates early so the user gets a
clear message.

## Not included

This sample has no installer, code signing, auto-update, tray integration,
focus trap, telemetry, or watch/doctor screens. Hardware behavior and the
WebView2 host itself still require validation on a Windows machine with a
rev-D board.
