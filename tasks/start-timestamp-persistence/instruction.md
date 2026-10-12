Native histogram start timestamps aren’t surviving durable storage yet. Can you add support so they survive restarts and compaction?

Put it behind `histograms-st-encoding`, independently of float encoding, and keep existing stored data readable.

Do not look for or use online solutions.
