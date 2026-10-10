After stale-series compaction and a later checkpoint, restarting Prometheus reports unknown WAL series references. The affected samples are already safely in a block, but the warnings make the database look corrupt. Can you track down why this happens and fix it?

Please make sure the fix doesn’t leave old series hanging around in later checkpoints.

Do not look for or use online solutions.
