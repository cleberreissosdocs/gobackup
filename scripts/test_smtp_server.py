#!/usr/bin/env python3
"""Minimal SMTP test server: accepts one session, speaks just enough SMTP
to satisfy Go's net/smtp.SendMail (HELO/EHLO, MAIL FROM, RCPT TO, DATA),
and writes every received message to a file for inspection."""
import socketserver
import sys
import datetime

OUTFILE = sys.argv[1] if len(sys.argv) > 1 else "/tmp/received_emails.txt"
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 1025


class SMTPHandler(socketserver.StreamRequestHandler):
    def send(self, line):
        self.wfile.write((line + "\r\n").encode())
        self.wfile.flush()

    def handle(self):
        self.send("220 gobackup-test-smtp ready")
        in_data = False
        data_lines = []
        while True:
            line = self.rfile.readline()
            if not line:
                break
            text = line.decode(errors="replace").rstrip("\r\n")
            if in_data:
                if text == ".":
                    in_data = False
                    with open(OUTFILE, "a") as f:
                        f.write(f"--- message received at {datetime.datetime.now()} ---\n")
                        f.write("\n".join(data_lines) + "\n\n")
                    self.send("250 OK: message queued")
                    data_lines = []
                else:
                    data_lines.append(text)
                continue

            upper = text.upper()
            if upper.startswith("HELO") or upper.startswith("EHLO"):
                self.send("250 gobackup-test-smtp")
            elif upper.startswith("MAIL FROM"):
                self.send("250 OK")
            elif upper.startswith("RCPT TO"):
                self.send("250 OK")
            elif upper.startswith("DATA"):
                in_data = True
                self.send("354 End data with <CR><LF>.<CR><LF>")
            elif upper.startswith("QUIT"):
                self.send("221 Bye")
                break
            else:
                self.send("250 OK")


if __name__ == "__main__":
    print(f"listening on 0.0.0.0:{PORT}, writing messages to {OUTFILE}")
    with socketserver.ThreadingTCPServer(("0.0.0.0", PORT), SMTPHandler) as server:
        server.allow_reuse_address = True
        server.serve_forever()
