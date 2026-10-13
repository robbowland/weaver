"""Run a command in a pty (as a terminal would) and type the hex-encoded bytes written to a FIFO."""
import os, pty, sys, select, struct, fcntl, termios

fifo, logpath = sys.argv[1], sys.argv[2]
cmd = sys.argv[3:]
pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack('HHHH', 50, 200, 0, 0))
    os.execvp(cmd[0], cmd)
ff = os.open(fifo, os.O_RDWR | os.O_NONBLOCK)
log = open(logpath, 'wb')
pending = b''
while True:
    r, _, _ = select.select([fd, ff], [], [], 0.5)
    if fd in r:
        try:
            data = os.read(fd, 65536)
        except OSError:
            break
        if not data:
            break
        if log.tell() > 2_000_000:
            log.seek(0); log.truncate()
        log.write(data); log.flush()
    if ff in r:
        pending += os.read(ff, 4096)
        while b'\n' in pending:
            line, pending = pending.split(b'\n', 1)
            line = line.strip()
            if line:
                os.write(fd, bytes.fromhex(line.decode()))
    if os.waitpid(pid, os.WNOHANG)[0]:
        break
