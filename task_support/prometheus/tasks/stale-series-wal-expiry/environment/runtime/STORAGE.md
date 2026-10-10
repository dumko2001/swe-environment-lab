# Stale-series storage playground

`task-env storage-demo /app/task-runtime/storage-examples/wal-demo-1` runs the public driver built by the last `task-env rebuild` through a real TSDB lifecycle. Use a new directory separate from the running Prometheus server. Existing directories are rejected, never deleted.

The driver appends a series that becomes stale and a series that remains active, compacts, advances time, and reopens the database. It prints stored samples and WAL/checkpoint records. Prometheus storage logs appear on stderr; the example does not guarantee a replay warning.

Inspect the retained checkpoint, WAL and block files, or modify the driver for another timestamp/lifecycle. This is a small public reproduction, not a grader: completing the program does not assert the absence of warnings or the correctness of checkpoint contents. It deliberately uses the real storage APIs rather than simulating checkpoint retention. A running scrape server alone would not deterministically trigger this sequence quickly.

After editing the repository or driver, run `task-env rebuild` before rerunning the example. This rebuilds both the server and the public driver against your edited source.
