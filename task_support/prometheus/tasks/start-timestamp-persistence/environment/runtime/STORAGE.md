# Histogram storage playground

`task-env storage-demo /app/task-runtime/storage-examples/histogram-demo-1` runs the public driver built by the last `task-env rebuild` and exercises real TSDB APIs in a new, separate directory. The running Prometheus server does not own this directory. Existing directories are rejected, never erased.

The driver submits integer and float histograms with start timestamp 60000, reports iterator observations in the head, reopens the WAL, compacts the head to a real block, and reopens again. Logs go to stderr; observations go to stdout. The baseline executes even though start timestamps may disappear. A successful process exit means the lifecycle completed, not that timestamp persistence is correct.

The default demonstration uses the existing `st-storage,xor2-encoding` features. To exercise histogram persistence through the public feature configuration, run:

```sh
STORAGE_FEATURES=histograms-st-encoding,xor2-encoding task-env storage-demo /app/task-runtime/storage-examples/histogram-demo-2
```

`STORAGE_FEATURES` is a comma-separated list of public feature names; `st-storage` is always included. The pinned TSDB requires XOR2 for global start-timestamp storage, so the lifecycle example includes it. The histogram feature remains a separate opt-in: enabling XOR2 alone does not enable histogram start-timestamp persistence. These settings use the repository's feature parser and configuration conversion; no implementation-specific option name is needed. On the starting implementation, an unsupported feature may be ignored and timestamps may still disappear.


Inspect the preserved `wal/`, `chunks_head/`, and block directories. HTTP query JSON does not expose the full start-timestamp storage contract; the driver reads actual TSDB iterator start timestamps. This small public example is not the private verifier and does not cover every storage path.

After editing the repository or driver, run `task-env rebuild` before rerunning the example. This rebuilds both the server and the public driver against your edited source.
