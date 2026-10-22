"""A fake Linear GraphQL API for the tmux suite: key lin_fake signs in as Rob
at Acme; the Queue section finds ENG-42, the other sections nothing. Writes
its port to the file named, then serves until killed."""
import json, socketserver, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

ISSUE = {
    "identifier": "ENG-42", "title": "Fix it", "description": "Broken.\n\n## Steps\n\n- open it\n- **see** it fail",
    "url": "https://linear.app/acme/issue/ENG-42/fix-it", "branchName": "t/eng-42-fix-it",
    "priority": 2, "priorityLabel": "High", "state": {"name": "Todo", "type": "unstarted"},
    "team": {"key": "ENG", "name": "Engineering"}, "assignee": {"name": "Rob", "displayName": "rob"},
    "creator": {"name": "Eve", "displayName": "eve"}, "labels": {"nodes": [{"name": "bug"}]},
    "project": None, "updatedAt": "2026-10-16T10:00:00Z",
}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def answer(self, code, body):
        b = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_POST(self):
        q = json.loads(self.rfile.read(int(self.headers["Content-Length"])))["query"]
        if self.headers.get("Authorization") != "lin_fake":
            return self.answer(400, {"errors": [{"message": "Authentication required, not authenticated"}]})
        if "viewer" in q:
            return self.answer(200, {"data": {"viewer": {"name": "Rob", "organization": {"name": "Acme"}}}})
        if "issue(id:" in q:
            return self.answer(200, {"data": {"issue": dict(ISSUE, comments={"nodes": [{"body": "Seen it", "user": {"name": "Eve", "displayName": "eve"}}]})}})
        # ENG-42 is assigned to you and to do (unstarted): Queue's, as Linear
        # would answer it, not Backlog's (triage or backlog).
        nodes = [ISSUE] if "assignee: { isMe: { eq: true } }" in q and '"unstarted"' in q else []
        return self.answer(200, {"data": {"issues": {"nodes": nodes}}})


class Server(HTTPServer):
    # HTTPServer names itself with socket.getfqdn, a reverse DNS lookup that
    # can take longer than the suite waits (GitHub's macOS runners).
    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        self.server_name, self.server_port = "127.0.0.1", self.server_address[1]


s = Server(("127.0.0.1", 0), H)
with open(sys.argv[1], "w") as f:
    f.write(str(s.server_address[1]))
s.serve_forever()
