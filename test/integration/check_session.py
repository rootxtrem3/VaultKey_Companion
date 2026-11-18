#!/usr/bin/env python3
"""Long-lived session protocol checks against the PTY simulator.

This drives `vaultkey session` exactly like the desktop bridge: one process,
JSON Lines over stdin/stdout, several commands over the same device open. It is
test-only scaffolding and never touches hardware.
"""
import json
import os
import select
import subprocess
import sys
import tempfile
import time
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
VK = os.environ.get("VK", os.path.join(ROOT, "bin", "vaultkey"))
WORK = os.environ.get("VK_WORK", os.path.join(os.environ.get("TMPDIR", "/tmp"), "vaultkey-it"))
PORT = os.environ.get("PORT", os.path.join(WORK, "tty"))
STATE = os.environ.get("VK_STATE", os.path.join(WORK, "state.json"))
LOG = os.environ.get("VK_LOG", os.path.join(WORK, "sim.log"))

PASS = 0
FAIL = 0


def check(name, condition, detail=""):
    global PASS, FAIL
    if condition:
        PASS += 1
        print("PASS  " + name)
        return True
    FAIL += 1
    print("FAIL  " + name)
    if detail:
        print("      " + str(detail)[:600].replace("\n", "\n      "))
    return False


def set_state(**values):
    current = {"locked": False, "authorized": True, "drop": ""}
    current.update(values)
    with open(STATE, "w") as handle:
        json.dump(current, handle)


class SessionError(Exception):
    pass


class Session:
    """One long-lived vaultkey session process."""

    def __init__(self):
        self.stderr_file = tempfile.TemporaryFile()
        self.proc = subprocess.Popen(
            [VK, "--port", PORT, "session"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=self.stderr_file,
            bufsize=0,
        )
        self.buffer = b""
        self.events = []
        self.raw_lines = []

    def send_raw(self, data):
        self.proc.stdin.write(data)
        self.proc.stdin.flush()

    def send(self, request_id, method, params=None, timeout_ms=None):
        message = {"id": request_id, "method": method}
        if params is not None:
            message["params"] = params
        if timeout_ms is not None:
            message["timeout_ms"] = timeout_ms
        self.send_raw((json.dumps(message, separators=(",", ":")) + "\n").encode())

    def read_line(self, timeout):
        deadline = time.time() + timeout
        while b"\n" not in self.buffer:
            remaining = deadline - time.time()
            if remaining <= 0:
                raise SessionError("timed out waiting for session output")
            ready, _, _ = select.select([self.proc.stdout], [], [], remaining)
            if not ready:
                raise SessionError("timed out waiting for session output")
            chunk = os.read(self.proc.stdout.fileno(), 65536)
            if not chunk:
                raise SessionError("session exited while waiting for output")
            self.buffer += chunk
        line, self.buffer = self.buffer.split(b"\n", 1)
        text = line.decode("utf-8", "replace")
        self.raw_lines.append(text)
        try:
            return json.loads(text)
        except json.JSONDecodeError as error:
            raise SessionError("non-JSON session output %r: %s" % (text, error))

    def read_until(self, predicate, timeout=10):
        deadline = time.time() + timeout
        while True:
            message = self.read_line(max(0.05, deadline - time.time()))
            if predicate(message):
                return message
            self.events.append(message)

    def request(self, request_id, method, params=None, timeout_ms=None, timeout=10):
        self.send(request_id, method, params, timeout_ms)
        return self.read_until(
            lambda message: message.get("type") == "response"
            and message.get("id") == request_id,
            timeout,
        )

    def ready(self, timeout=10):
        return self.read_until(
            lambda message: message.get("type") == "event"
            and message.get("event") == "ready",
            timeout,
        )

    def wait(self, timeout=5):
        try:
            return self.proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            return self.proc.wait()

    def stderr_text(self):
        try:
            self.stderr_file.seek(0)
            return self.stderr_file.read().decode("utf-8", "replace")
        except Exception:
            return ""

    def close(self):
        if self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait()
        for stream in (self.proc.stdin, self.proc.stdout):
            try:
                stream.close()
            except Exception:
                pass
        self.stderr_file.close()


def error_of(message):
    value = message.get("error")
    return value if isinstance(value, dict) else {}


def result_of(message):
    value = message.get("result")
    return value if isinstance(value, dict) else {}


def session_log_commands():
    try:
        with open(LOG) as handle:
            return [line.strip() for line in handle if line.strip()]
    except FileNotFoundError:
        return []


def main():
    if not os.path.exists(PORT):
        print("no simulator at %s" % PORT, file=sys.stderr)
        return 1

    set_state()
    session = Session()
    primary_error = None
    try:
        ready = session.ready()
        value = result_of(ready)
        check("ready event", ready.get("event") == "ready", ready)
        check("ready session protocol", value.get("session_protocol") == 1, value)
        check("ready reports port", str(value.get("port", "")).endswith("tty"), value)
        device = value.get("device")
        check("ready reports device",
              isinstance(device, dict) and device.get("product") == "VaultKey", value)

        response = session.request(1, "capabilities")
        capabilities = result_of(response)
        check("capabilities ok", response.get("ok") is True, response)
        check("capabilities session protocol",
              capabilities.get("session_protocol") == 1, capabilities)
        check("capabilities lists status",
              "status" in (capabilities.get("methods") or []), capabilities)

        response = session.request(2, "status")
        status = result_of(response)
        check("status ok", response.get("ok") is True, response)
        check("status authorized and unlocked",
              status.get("provisioning") is True and status.get("locked") is False, status)
        check("status entry counts", status.get("credentials") == 2 and status.get("totp") == 1,
              status)

        response = session.request(3, "list", {"all": True})
        listing = result_of(response)
        check("list ok", response.get("ok") is True, response)
        check("list aggregates entries",
              listing.get("credential_total") == 2 and listing.get("totp_total") == 1
              and len(listing.get("credentials") or []) == 2, listing)

        response = session.request(4, "time.get")
        clock = result_of(response)
        check("time.get ok", response.get("ok") is True, response)
        check("time.get returns valid unix", isinstance(clock.get("unix"), int)
              and clock.get("unix", 0) > 0 and clock.get("utc") is True, clock)

        response = session.request(5, "doctor")
        report = result_of(response)
        check("doctor ok", response.get("ok") is True and report.get("ok") is True, response)
        check("doctor checks present", len(report.get("checks") or []) >= 5, report)

        response = session.request(6, "credentials.add", {
            "service": "Session Test", "username": "tester",
            "password": "session-secret", "url": "", "notes": "",
        })
        added = result_of(response)
        check("credentials.add ok", response.get("ok") is True, response)
        entry_id = added.get("entry_id")
        check("credentials.add returns id", isinstance(entry_id, int) and entry_id > 0, added)

        if isinstance(entry_id, int) and entry_id > 0:
            response = session.request(7, "delete", {"type": "credential", "id": entry_id})
            deleted = result_of(response)
            check("delete ok", response.get("ok") is True and deleted.get("deleted") is True,
                  response)

        response = session.request(8, "watch.start", {"interval_ms": 250})
        check("watch.start ok", response.get("ok") is True, response)
        watch_event = session.read_until(
            lambda message: message.get("type") == "event"
            and message.get("event") == "status",
            5,
        )
        watch_status = result_of(watch_event)
        check("watch emits status events",
              isinstance(watch_status.get("status"), dict), watch_event)
        response = session.request(9, "watch.stop")
        check("watch.stop ok", response.get("ok") is True, response)

        session.send_raw(b"this is not json\n")
        response = session.read_until(
            lambda message: message.get("type") == "response" and bool(error_of(message)), 5)
        check("malformed line errors", error_of(response).get("code") == "invalid_json", response)

        session.send_raw(b"x" * (70 * 1024) + b"\n")
        response = session.read_until(
            lambda message: message.get("type") == "response" and bool(error_of(message)), 10)
        check("oversized line errors", error_of(response).get("code") == "line_too_long", response)

        commands = session_log_commands()
        check("one device open across commands", commands.count("HELLO") == 1,
              "simulator commands: %s" % commands[:12])

        set_state(drop="ADD_CREDENTIAL")
        response = session.request(10, "credentials.add", {
            "service": "Ambiguous", "password": "session-secret",
        }, timeout_ms=700, timeout=10)
        info = error_of(response)
        check("ambiguous mutation is surfaced",
              info.get("kind") == "ambiguous" and info.get("ambiguous") is True
              and info.get("code") == "ambiguous_write" and info.get("exit_code") == 7,
              response)
        set_state()

        response = session.request(11, "shutdown")
        check("shutdown response",
              response.get("ok") is True
              and result_of(response).get("shutting_down") is True, response)
        code = session.wait()
        check("shutdown exits cleanly", code == 0, "exit %d stderr=%s" % (
            code, session.stderr_text()))
    except Exception as error:
        primary_error = error
        check("primary session completed", False, "%s\n%s" % (error, session.stderr_text()))
    finally:
        session.close()

    if primary_error is None:
        # EOF on stdin must close the device and exit cleanly.
        eof_session = Session()
        try:
            eof_session.ready()
            eof_session.proc.stdin.close()
            code = eof_session.wait()
            check("EOF exits cleanly", code == 0,
                  "exit %d stderr=%s" % (code, eof_session.stderr_text()))
        except Exception as error:
            check("EOF exits cleanly", False, "%s\n%s" % (error, eof_session.stderr_text()))
        finally:
            eof_session.close()

        # Unauthorized and locked states must map to stable error kinds.
        try:
            set_state(authorized=False)
            unauthorized = Session()
            try:
                unauthorized.ready()
                response = unauthorized.request(1, "status")
                check("unauthorized status ok", response.get("ok") is True, response)
                response = unauthorized.request(2, "list", {})
                info = error_of(response)
                check("unauthorized list maps kind",
                      info.get("kind") == "not_authorized"
                      and info.get("code") == "provisioning_not_authorized"
                      and info.get("exit_code") == 5, response)
                unauthorized.request(3, "shutdown")
                check("unauthorized session exits cleanly", unauthorized.wait() == 0)
            finally:
                unauthorized.close()
        except Exception as error:
            check("unauthorized session", False, str(error))

        try:
            set_state(locked=True, authorized=False)
            locked = Session()
            try:
                locked.ready()
                response = locked.request(1, "status")
                status = result_of(response)
                check("locked status ok",
                      response.get("ok") is True and status.get("locked") is True, response)
                response = locked.request(2, "list", {})
                info = error_of(response)
                check("locked list maps kind",
                      info.get("kind") == "locked" and info.get("code") == "device_locked"
                      and info.get("exit_code") == 4, response)
                locked.request(3, "shutdown")
                check("locked session exits cleanly", locked.wait() == 0)
            finally:
                locked.close()
        except Exception as error:
            check("locked session", False, str(error))
        finally:
            set_state()

    print()
    print("RESULT: %d passed, %d failed" % (PASS, FAIL))
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
