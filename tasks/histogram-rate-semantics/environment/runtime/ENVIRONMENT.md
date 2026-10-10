# Live Prometheus development environment

A real Prometheus built from your starting source is running at http://127.0.0.1:9090.
Harbor starts it before your session. The missing feature may still fail; readiness
only means the baseline deployment is available.

- `task-env status` shows health, public data timestamps and scrape targets.
- `task-env logs` shows actual process logs.
- `task-env rebuild` builds your changed source and restarts it, preserving data.
- `task-env restart` restarts the current binary, preserving data.
- `task-env stop` stops the deployment. `task-env start` starts it again.
- `task-env reset-data` explicitly deletes the public demo TSDB and reseeds it.

Source: `/app/prometheus`. Configuration: `/app/task-runtime/prometheus.yml`.
Data, WAL and logs: `/app/task-runtime`. Public tool source: `/opt/task-runtime`.
Runtime files are outside the submitted code tree. Edit the configuration and
POST to `http://127.0.0.1:9090/-/reload` to reload it.

Public samples include `metric`, `arg`, `demo_counter`, `demo_histogram`,
`demo_float_histogram`, and `mixed_types`. The histogram histories include a reset
and schema transition; mixed_types changes sample type. These are real TSDB samples,
not mocked query responses. Recent sample bounds are in `/app/task-runtime/seed.json`.
There is also a fixed historical window at Unix seconds 0–1800 for the reported
`@ 2m` subquery. Always supply an explicit evaluation time for seeded data:

```
curl -G http://127.0.0.1:9090/api/v1/query --data-urlencode 'query=metric' --data-urlencode time=120
curl -G http://127.0.0.1:9090/api/v1/query_range --data-urlencode 'query=quantile_over_time(scalar(arg), metric[2m1s:30s] @ 2m)' --data-urlencode start=120 --data-urlencode end=240 --data-urlencode step=30
```

The Unix-socket profile additionally runs three controlled metrics exporters on
alpha.sock, beta.sock and secure.sock. The HTTPS endpoint uses the public local
certificate in the runtime directory. Their deliberately shared advertised address
exercises the real scraper's socket routing. Down targets on the baseline are expected.

Storage profiles additionally provide `task-env storage-demo [NEW_DIR]` and
`/opt/task-runtime/STORAGE.md`. These invoke the real candidate TSDB lifecycle;
query HTTP responses alone do not expose all storage semantics. Each run uses a
fresh separate directory and keeps its artifacts for inspection.
