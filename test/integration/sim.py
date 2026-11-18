#!/usr/bin/env python3
"""Test-only VaultKey CDC protocol simulator.

This is integration-test scaffolding, not production code. It creates a real
PTY pair, links the slave path to /tmp/opencode/vk/tty, and speaks the JSON
Lines protocol from docs/USB_PROTOCOL.md. It has no cryptography, no real
secrets, and never touches hardware. State is read from state.json on every
request so tests can switch locked / authorized / drop modes.
"""
import json
import os
import pty
import random
import select
import sys
import time
import tty

WORK = os.environ.get("VK_WORK", os.path.join(os.environ.get("TMPDIR", "/tmp"), "vaultkey-it"))
LINK = os.environ.get("VK_LINK", os.path.join(WORK, "tty"))
STATE = os.environ.get("VK_STATE", os.path.join(WORK, "state.json"))
LOG = os.environ.get("VK_LOG", os.path.join(WORK, "sim.log"))

def new_id():
    # The firmware allocates random non-zero 32-bit IDs, not sequences.
    while True:
        value = random.getrandbits(32)
        if value != 0:
            return value


credentials = [
    {"id": new_id(), "service": "GitHub", "username": "test-user",
     "password": "test-password", "url": "https://github.test", "notes": "test"},
    {"id": new_id(), "service": "Example", "username": "user@example.test",
     "password": "example-password", "url": "", "notes": ""},
]
totp = [{"id": new_id(), "issuer": "Example", "account": "user@example.test",
         "uri": "otpauth://totp/Example:user?secret=JBSWY3DPEHPK3PXP"}]
clock = 1794528000


def state():
    try:
        with open(STATE) as handle:
            current = json.load(handle)
    except Exception:
        current = {}
    current.setdefault("locked", False)
    current.setdefault("authorized", True)
    current.setdefault("drop", "")
    return current


def save(current):
    with open(STATE, "w") as handle:
        json.dump(current, handle)


def log(message):
    with open(LOG, "a") as handle:
        handle.write(message + "\n")


def status():
    current = state()
    authorized = current["authorized"] and not current["locked"]
    result = {
        "v": 1, "ok": True,
        "locked": current["locked"],
        "rtc": "ready",
        "storage": "ready",
        "startup": "locked_home" if current["locked"] else "home",
        "display": "ready",
        "button_event_drops": 0,
        "usb_driver": True,
        "usb_mounted": True,
        "cdc": True,
        "hid": True,
        "provisioning": authorized,
    }
    if authorized:
        result["credentials"] = len(credentials)
        result["totp"] = len(totp)
    return result


def error(request_id, code):
    return {"v": 1, "id": request_id, "ok": False, "error": code}


def handle(request):
    global clock
    command = request.get("cmd", "")
    request_id = request.get("id")
    current = state()
    if current.get("drop") == command:
        # Simulate a response lost in transit without blocking other commands.
        return None
    if command in ("HELLO", "GET_VERSION"):
        return {"v": 1, "id": request_id, "ok": True, "product": "VaultKey",
                "firmware": "0.1.0-prototype", "hardware": "rev-D", "protocol": 1}
    if command == "GET_STATUS":
        result = status()
        result["id"] = request_id
        return result
    if command == "GET_TIME":
        return {"v": 1, "id": request_id, "ok": True, "unix": clock, "utc": True}
    if command == "LOCK_DEVICE":
        current["locked"] = True
        current["authorized"] = False
        save(current)
        return {"v": 1, "id": request_id, "ok": True}
    if current["locked"]:
        return error(request_id, "device_locked")
    if not current["authorized"]:
        return error(request_id, "provisioning_not_authorized")

    if command == "SET_TIME":
        clock = int(request.get("unix", 0))
        if clock <= 0:
            return error(request_id, "invalid_time")
        return {"v": 1, "id": request_id, "ok": True, "unix": clock}
    if command == "LIST_SERVICES":
        query = str(request.get("query", "") or "").lower()
        offset = int(request.get("offset", 0) or 0)
        limit = int(request.get("limit", 0) or 0) or 20
        if offset > 256 or limit > 25:
            return error(request_id, "invalid_pagination")
        matched_credentials = [c for c in credentials if query in c["service"].lower()]
        matched_totp = [t for t in totp
                        if query in t["issuer"].lower() or query in t["account"].lower()]
        return {
            "v": 1, "id": request_id, "ok": True,
            "offset": offset, "limit": limit,
            "credential_total": len(matched_credentials),
            "totp_total": len(matched_totp),
            "credentials": [{"id": c["id"], "service": c["service"]}
                            for c in matched_credentials[offset:offset + limit]],
            "totp": [{"id": t["id"], "issuer": t["issuer"], "account": t["account"]}
                     for t in matched_totp[offset:offset + limit]],
        }
    if command == "ADD_CREDENTIAL":
        fields = ("service", "username", "password", "url", "notes")
        if not request.get("service") or not request.get("password"):
            return error(request_id, "invalid_credential")
        entry_id = new_id()
        entry = {"id": entry_id}
        entry.update({name: request.get(name, "") for name in fields})
        credentials.append(entry)
        return {"v": 1, "id": request_id, "ok": True, "entry_id": entry_id}
    if command == "UPDATE_CREDENTIAL":
        found = next((c for c in credentials if c["id"] == request.get("entry_id")), None)
        if found is None:
            return error(request_id, "entry_not_found")
        fields = ("service", "username", "password", "url", "notes")
        found.update({name: request.get(name, "") for name in fields})
        return {"v": 1, "id": request_id, "ok": True, "entry_id": found["id"]}
    if command == "ADD_TOTP":
        uri = request.get("uri", "")
        if not uri.startswith("otpauth://totp/"):
            return error(request_id, "invalid_totp")
        entry_id = new_id()
        totp.append({"id": entry_id, "issuer": "Imported", "account": "account", "uri": uri})
        return {"v": 1, "id": request_id, "ok": True, "entry_id": entry_id}
    if command == "DELETE_ENTRY":
        kind = request.get("type")
        entry_id = request.get("entry_id")
        if kind == "credential":
            remaining = [c for c in credentials if c["id"] != entry_id]
        elif kind == "totp":
            remaining = [t for t in totp if t["id"] != entry_id]
        else:
            return error(request_id, "invalid_entry_type")
        if len(remaining) == (len(credentials) if kind == "credential" else len(totp)):
            return error(request_id, "delete_failed")
        if kind == "credential":
            credentials[:] = remaining
        else:
            totp[:] = remaining
        return {"v": 1, "id": request_id, "ok": True}
    return error(request_id, "unknown_command")


def main():
    master, slave = pty.openpty()
    tty.setraw(slave)
    os.makedirs(os.path.dirname(os.path.abspath(LINK)), exist_ok=True)
    link = os.path.abspath(LINK)
    try:
        os.unlink(link)
    except FileNotFoundError:
        pass
    os.symlink(os.ttyname(slave), link)
    print(f"listening on {link} -> {os.ttyname(slave)}", flush=True)

    buffer = b""
    while True:
        ready, _, _ = select.select([master], [], [])
        if not ready:
            continue
        try:
            chunk = os.read(master, 4096)
        except OSError:
            time.sleep(0.05)
            continue
        if not chunk:
            continue
        buffer += chunk
        while b"\n" in buffer:
            line, buffer = buffer.split(b"\n", 1)
            line = line.strip()
            if not line:
                continue
            try:
                request = json.loads(line)
            except Exception:
                response = {"v": 1, "ok": False, "error": "invalid_json"}
            else:
                log(request.get("cmd", "<missing>"))
                response = handle(request)
            if response is None:
                continue
            payload = json.dumps(response, separators=(",", ":")).encode() + b"\n"
            os.write(master, payload)


if __name__ == "__main__":
    main()
