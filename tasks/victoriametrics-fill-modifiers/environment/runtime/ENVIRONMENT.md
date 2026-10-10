# Live VictoriaMetrics environment

A real single-node VictoriaMetrics server runs as `agent` at
`http://127.0.0.1:8428`. Its binary is built from `/app/victoriametrics`.
Harbor's environment healthcheck starts it before the agent begins; the Docker
entrypoint starts it when running the image directly.

Run `task-env status` to check readiness or `task-env logs` for recent output.
`task-env rebuild` compiles your current source, replaces the running binary only
on successful compilation, and restarts it. `task-env restart` restarts the
existing binary. Both preserve the data. `task-env stop` stops the server;
`task-env start` is idempotent and restores it. `task-env reset-data` explicitly
**deletes the playground's stored data** and imports a fresh example dataset.
Use this command to move the examples to the current time again.

Data, logs, PID and the replaceable binary live under `/app/task-runtime`, outside
the submitted source tree. The initial data consists of public demonstration
series `demo_requests` and `demo_capacity`, with one shared host and a host unique
to each metric. Their sample timestamps are staggered. These are sample inputs
imported into actual storage; HTTP queries execute the actual candidate engine.
They are not the private evaluation dataset or an exhaustive correctness test.

Use the saved explicit time range so examples remain reproducible:

```sh
. /app/task-runtime/query.env
curl -fsSG http://127.0.0.1:8428/api/v1/query_range \
  --data-urlencode 'query=demo_requests' \
  --data-urlencode "start=$START" --data-urlencode "end=$END" \
  --data-urlencode "step=$STEP"
curl -fsSG http://127.0.0.1:8428/api/v1/query \
  --data-urlencode 'query=demo_requests - demo_capacity' \
  --data-urlencode "time=$END"
curl -fsSG http://127.0.0.1:8428/api/v1/query \
  --data-urlencode 'query=demo_requests - fill(0) demo_capacity' \
  --data-urlencode "time=$END"
```

The baseline intentionally lacks the requested fill behavior. Use instant and
range queries while developing it. Ordinary raw selectors apply the engine's
lookback behavior; staggered raw samples do not imply a missing result at every
unrecorded timestamp. Inspect the imported samples through `/api/v1/export` and
use explicit rollup windows when studying per-timestamp missingness.

Additional JSON-lines series can be POSTed to `/api/v1/import`; call
`/internal/force_flush` before querying them immediately. The server's normal
`/metrics`, `/health` and query endpoints are available. No credentials are
required on the loopback interface.
