Zooming out on a dashboard keeps hitting Query even when we've already cached a denser response. Can you make Query Frontend reuse that data? Fetch only the missing parts, and keep results correct when people zoom back and forth.

We need this for steps of 1, 5, 10, 15, 20 and 30 seconds; 1, 2, 5, 10, 15 and 30 minutes; and 1, 2, 3, 6 and 12 hours. Reuse cached coverage whenever it contains the requested timestamps; fetch fresh results where the timestamps don’t line up.

Keep cache traffic bounded: for an unsplit request to one cache server, checking the ordinary key and compatible entries should take no more than two read commands. Backend changes are enough; no UI work is needed.

Do not look for or use online solutions.
