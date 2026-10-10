Can you extend anchored and smoothed rate, increase and delta to native histograms, and support smoothed histogram selectors too? They currently only work for floats.

Also, a bare `mixed_types smoothed` instant query can terminate the query, while wrapping it in `sort(...)` gives a normal warning. Please fix that as part of this.

Do not look for or use online solutions.
