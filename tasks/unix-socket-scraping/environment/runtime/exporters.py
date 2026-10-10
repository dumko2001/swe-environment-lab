#!/usr/bin/env python3
"""Public HTTP/TLS metrics inputs over separate Unix sockets."""
import http.server
import socketserver
import ssl
import threading
import time
from pathlib import Path
ROOT = Path('/app/task-runtime')
class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        data = ('# TYPE demo_exporter_value gauge\ndemo_exporter_value{exporter="'+self.server.label+'"} '+str(self.server.value)+'\n').encode()
        self.send_response(200); self.send_header('Content-Type','text/plain; version=0.0.4'); self.send_header('Content-Length',str(len(data))); self.end_headers(); self.wfile.write(data)
    def log_message(self,*args): pass
for label,value,tls in [('alpha',11,False),('beta',29,False),('secure',47,True)]:
    path=ROOT/(label+'.sock');path.unlink(missing_ok=True)
    server=Server(str(path),Handler);server.label=label;server.value=value
    if tls:
        ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain(str(ROOT/'cert.pem'),str(ROOT/'key.pem'));server.socket=ctx.wrap_socket(server.socket,server_side=True)
    threading.Thread(target=server.serve_forever,daemon=True).start()
while True: time.sleep(3600)
