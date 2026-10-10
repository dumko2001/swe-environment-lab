# Private behavioral rubric

The score is 1 only when the candidate builds with the pinned dependency graph and every behavioral assertion passes; otherwise it is 0. Missing collected artifacts are an infrastructure error, not an agent failure. Per-case diagnostics remain available even though the final score is binary.

Each family runs against three independently generated fixtures (135 assertions total):

| Criterion | Evidence |
| --- | --- |
| Missing operands | Symmetric and asymmetric fill values; right-only and left-only groups; empty left/right; both empty; wrong-side modifier does not synthesize results. |
| Query evaluation | Imported, differing label sets expose optimizer pushdown mistakes; nested aggregation checks composition. |
| Matching and labels | `on`, `ignoring`, `group_left`, `group_right`, copied labels with multiple matching series, per-pair missingness, disjoint-series merging, invalid-cardinality errors, and preserved metric names. |
| Missing samples | Time-varying vectors and scalars produce left-only, both-absent, and right-only intervals. NaN operands and NaN/Inf fill values preserve absence and infinity semantics. |
| Comparisons | Filter comparisons retain the qualifying operand value and the correct source metric name; `bool` comparisons return the proper truth value. |
| Compatibility | Ordinary intersection, scalar arithmetic, logical matching, `default`, exact label sets, exact timestamps, and instant/range agreement. |

The controller computes numerical and label expectations directly from generated fixture values. It does not invoke the reference solution, inspect implementation strings, require a particular function, or score candidate-authored test output. Valid implementations may trade some filter-pushdown performance for correctness; the alternate fixture demonstrates that such a solution passes. No latency threshold or arbitrary code shape is graded.

The September 8 expansion adds 21 semantic checks per fixture. The earlier 72-check results remain historical results; replaying an old submission against these additional checks is explicitly a diagnostic replay, not a fresh model score. See `calibration/SEPT8_HARDENING.md`.
