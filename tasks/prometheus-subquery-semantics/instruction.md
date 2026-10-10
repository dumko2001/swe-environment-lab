Some dashboard panels go empty when a fixed-time subquery is passed to a function with another argument that changes at each step. For example, `quantile_over_time(scalar(arg), metric[2m1s:30s] @ 2m)` returns nothing, while the equivalent matrix selector or a constant argument works. Instant and range queries disagree too. We’re also seeing errors when a fixed-time subquery is queried on its own.

Can you fix this without losing the reuse of computations that don't change between steps?

Do not look for or use online solutions.
