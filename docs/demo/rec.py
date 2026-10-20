"""Record a command in a pseudo-terminal as an asciicast (v2), typing the
hex-encoded bytes written to a FIFO, one key per line.
  python3 -I rec.py FIFO OUT.cast COLS ROWS COMMAND..."""
import codecs, fcntl, json, os, pty, select, struct, sys, termios, time

fifo, out, cols, rows = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
cmd = sys.argv[5:]
pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
    os.execvp(cmd[0], cmd)
ff = os.open(fifo, os.O_RDWR | os.O_NONBLOCK)
cast = open(out, 'w')
head = {'version': 2, 'width': cols, 'height': rows, 'timestamp': int(time.time()),
        'idle_time_limit': 3, 'env': {'TERM': 'xterm-256color', 'SHELL': '/bin/bash'}}
cast.write(json.dumps(head) + '\n')
start = time.monotonic()
dec = codecs.getincrementaldecoder('utf-8')('replace')
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
        text = dec.decode(data)
        if text:
            cast.write(json.dumps([round(time.monotonic() - start, 6), 'o', text]) + '\n')
            cast.flush()
    if ff in r:
        pending += os.read(ff, 4096)
        while b'\n' in pending:
            line, pending = pending.split(b'\n', 1)
            line = line.strip()
            if line:
                os.write(fd, bytes.fromhex(line.decode()))
    if os.waitpid(pid, os.WNOHANG)[0]:
        break
