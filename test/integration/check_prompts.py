#!/usr/bin/env python3
"""Drive the hidden-password prompt through a real PTY.

Checks: the secret is not echoed, the prompt reads two lines, terminal echo is
restored, Ctrl-C restores the terminal and exits 130, and --debug does not log
the secret.
"""
import fcntl
import os
import pty
import select
import signal
import sys
import termios
import time

VK = os.environ.get("VK", os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                       "..", "..", "bin", "vaultkey"))
WORK = os.environ.get("VK_WORK", os.path.join(os.environ.get("TMPDIR", "/tmp"), "vaultkey-it"))
PORT = os.environ.get("PORT", os.path.join(WORK, "tty"))
PASSED = 0
FAILED = 0


def check(name, condition, detail=""):
    global PASSED, FAILED
    if condition:
        PASSED += 1
        print(f"PASS  {name}")
    else:
        FAILED += 1
        print(f"FAIL  {name} {detail}")


def spawn(arguments):
    master, slave = pty.openpty()
    pid = os.fork()
    if pid == 0:
        os.close(master)
        os.setsid()
        fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        for descriptor in (0, 1, 2):
            os.dup2(slave, descriptor)
        os.close(slave)
        os.execv(VK, [VK, "--port", PORT] + arguments)
        os._exit(127)
    # Keep the slave open so the parent can inspect termios after the child
    # exits and so master reads do not fail with EIO.
    return master, pid, slave


def read_until(master, needle, timeout=5.0):
    data = b""
    deadline = time.time() + timeout
    while needle.encode() not in data:
        remaining = deadline - time.time()
        if remaining <= 0:
            break
        ready, _, _ = select.select([master], [], [], remaining)
        if not ready:
            break
        try:
            chunk = os.read(master, 4096)
        except OSError:
            break
        if not chunk:
            break
        data += chunk
    return data


def drain(master, timeout=5.0):
    data = b""
    deadline = time.time() + timeout
    while True:
        remaining = deadline - time.time()
        if remaining <= 0:
            break
        ready, _, _ = select.select([master], [], [], remaining)
        if not ready:
            break
        try:
            chunk = os.read(master, 4096)
        except OSError:
            break
        if not chunk:
            break
        data += chunk
    return data


def wait_status(pid, timeout=10.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result, status = os.waitpid(pid, os.WNOHANG)
        if result == pid:
            return os.waitstatus_to_exitcode(status)
        time.sleep(0.01)
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    return None


def main():
    os.environ["VAULTKEY_PORT"] = PORT

    # Successful add with two hidden prompts.
    master, pid, slave = spawn(["add-credential", "--service", "Prompt Test",
                                "--username", "prompt-user", "--debug"])
    transcript = read_until(master, "Password:")
    during = termios.tcgetattr(slave)
    os.write(master, b"prompt-secret-value\n")
    transcript += read_until(master, "Confirm password:")
    os.write(master, b"prompt-secret-value\n")
    transcript += drain(master)
    status = wait_status(pid)
    text = transcript.decode("utf-8", "replace")
    after = termios.tcgetattr(slave)
    os.close(master)
    os.close(slave)
    check("hidden prompt add exits 0", status == 0, f"status={status}")
    check("echo disabled during prompt", not during[3] & termios.ECHO)
    check("echo restored after prompt", after[3] & termios.ECHO)
    check("prompt labels shown", "Password:" in text and "Confirm password:" in text)
    check("secret not echoed", "prompt-secret-value" not in text)
    check("debug log has no secret", "prompt-secret-value" not in text)
    check("entry id printed", "Added credential #" in text, text)

    # Mismatched confirmation is rejected before any device write.
    master, pid, slave = spawn(["add-credential", "--service", "Prompt Mismatch"])
    transcript = read_until(master, "Password:")
    os.write(master, b"first-secret\n")
    transcript += read_until(master, "Confirm password:")
    os.write(master, b"second-secret\n")
    transcript += drain(master)
    status = wait_status(pid)
    text = transcript.decode("utf-8", "replace")
    after = termios.tcgetattr(slave)
    os.close(master)
    os.close(slave)
    check("mismatched prompts exit 2", status == 2, f"status={status}")
    check("echo restored after mismatch", after[3] & termios.ECHO)
    check("mismatch message", "passwords do not match" in text, text)
    check("mismatch does not leak secrets",
          "first-secret" not in text and "second-secret" not in text)

    # Invalid non-secret fields are rejected before the secret prompt.
    master, pid, slave = spawn(["add-credential", "--username", "prompt-user"])
    transcript = drain(master)
    status = wait_status(pid)
    text = transcript.decode("utf-8", "replace")
    os.close(master)
    os.close(slave)
    check("invalid metadata exits 2", status == 2, f"status={status}")
    check("invalid metadata skips password prompt", "Password:" not in text, text)

    # Ctrl-C during the first prompt restores the terminal and exits 130.
    master, pid, slave = spawn(["add-credential", "--service", "Prompt Interrupt"])
    read_until(master, "Password:")
    os.kill(pid, signal.SIGINT)
    transcript = drain(master)
    status = wait_status(pid)
    after = termios.tcgetattr(slave)
    os.close(master)
    os.close(slave)
    check("prompt SIGINT exits 130", status == 130, f"status={status}")
    check("echo restored after SIGINT", after[3] & termios.ECHO)

    # TOTP URI prompt also hides input.
    master, pid, slave = spawn(["add-totp"])
    transcript = read_until(master, "TOTP URI:")
    os.write(master, b"otpauth://totp/Hidden:user?secret=JBSWY3DPEHPK3PXP\n")
    transcript += drain(master)
    status = wait_status(pid)
    text = transcript.decode("utf-8", "replace")
    after = termios.tcgetattr(slave)
    os.close(master)
    os.close(slave)
    check("hidden TOTP prompt exits 0", status == 0, f"status={status}")
    check("echo restored after TOTP", after[3] & termios.ECHO)
    check("TOTP secret not echoed", "JBSWY3DPEHPK3PXP" not in text)

    print()
    print(f"RESULT: {PASSED} passed, {FAILED} failed")
    return 0 if FAILED == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
