# Local Prometheus deployment

A real Prometheus server is available at `http://127.0.0.1:9090`. Its Targets page and `/api/v1/targets` show current scrape health. The initial server still exhibits the reported issue.

- `task-env status` reports readiness and targets.
- `task-env logs` shows service logs.
- `task-env rebuild` builds your changed source and restarts it, preserving data.
- `task-env restart` restarts the existing binary; `task-env stop` and `task-env start` control the deployment.
- `task-env reset-data` explicitly resets the public sample database.

Source is in `/app/prometheus`; config is `/app/task-runtime/prometheus.yml`. Runtime tools, logs and data live outside the submitted source tree, under `/opt/task-runtime` and `/app/task-runtime`. Edit config and POST to `http://127.0.0.1:9090/-/reload` to apply it.

The environment includes HTTP metrics exporters on `alpha.sock` and `beta.sock`, and an HTTPS exporter on `secure.sock`. Socket paths and the local TLS certificate are in the runtime directory. Their addresses, scrape labels and certificate settings are visible in the Prometheus configuration. They provide public sample metrics for inspecting the reported behavior. You can modify these public fixtures or run additional exporters while developing your fix.
