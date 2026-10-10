# Running Thanos playground

The actor starts real Prometheus → Thanos Sidecar → Query → candidate Query Frontend, with Memcached caching actual Query responses. Float counters and native histograms are ingested into Prometheus using its remote-write API; there are no Query or Store response mocks. Initial samples cover two hours ending an hour before first boot. Prometheus continues scraping itself.

`task-env status` lists the processes and sample timestamps. `task-env logs` shows recent logs. `task-env rebuild` builds your current `/app/thanos` source and restarts all Thanos components, preserving Prometheus data and Memcached. `task-env restart` restarts the whole stack (and clears the in-memory cache). `task-env reset-data` explicitly deletes only the public playground data and reseeds it relative to the current time. `task-env start` is idempotent; it verifies both float and histogram queries through the frontend.

Local endpoints: Prometheus `127.0.0.1:9090`, Sidecar HTTP `10902` / gRPC `10901`, Query HTTP `10904` / gRPC `10903`, Query Frontend `10906`, Memcached `11211`. Services bind loopback and need no credentials. Config is `/opt/task-env/prometheus.yml`; data, logs, and PID records are in `/var/lib/task-env`; binaries in `/opt/task-env/bin`. These are outside the submitted source directory.

Use this public query example, then repeat at steps 30 and 60 to explore dashboard zoom behavior:

```sh
python3 - <<'PY'
import json, urllib.parse, urllib.request
seed=json.load(open('/var/lib/task-env/seed.json'))
for step in (15,30,60,15):
    params=dict(seed,query='playground_requests_total',step=step)
    url='http://127.0.0.1:10906/api/v1/query_range?'+urllib.parse.urlencode(params)
    result=json.load(urllib.request.urlopen(url))
    print(step,result)
PY
```

Also query `playground_latency_seconds` for actual native histograms and `rate(playground_requests_total[2m])` for float rates. Query's `/metrics` endpoint exposes HTTP request counts and frontend `/metrics` exposes cache activity. Compare direct Query requests on port 10904 with frontend responses on 10906. The public seed is a development aid; it is not the private grading suite. The baseline starts and returns data while retaining the cache-reuse bug you must fix.

Prometheus runtime: upstream v3.5.0 Linux amd64 release, verified using the pinned SHA-256 in the Dockerfile. Thanos services are all built from the candidate source.
