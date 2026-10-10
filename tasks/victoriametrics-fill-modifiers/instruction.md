Our dashboards lose comparisons when one metric has gaps. Can you add support for `fill(v)`, `fill_left(v)` and `fill_right(v)` in binary expressions?

Use the supplied value for the missing side; `fill(v)` covers either side. Fill should cover a missing series as well as a gap in an existing one. If both sides are missing, leave the gap. This should also work inside larger expressions without changing the usual matching or comparison behavior.

Do not look for or use online solutions.
