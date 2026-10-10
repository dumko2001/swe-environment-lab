We're losing start timestamps when native histograms and float histograms go through durable storage. Can you make them survive restarts and compaction?

Put this behind `histograms-st-encoding`, independently of float encoding, and use the agreed format in `/app/histogram-st-format.md`. Existing stored data still needs to be readable.

Do not look for or use online solutions.
