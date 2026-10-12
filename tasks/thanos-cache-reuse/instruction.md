Zooming out on a dashboard keeps hitting Query even when we've already cached a denser response. Can you make Query Frontend reuse that data? Fetch only the missing parts, and keep results correct when people zoom back and forth.

Reuse cached coverage when it contains the requested timestamps; fetch fresh results where the timestamps don’t line up. Avoid a separate cache request for every interval you check.

Do not look for or use online solutions.
