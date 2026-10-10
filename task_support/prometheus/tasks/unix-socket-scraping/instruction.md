We have exporters that listen on Unix sockets. Can you add scraping through `__unix_socket__`? Several exporters may advertise the same host and port while using different sockets.

This needs to work with HTTPS and config reloads, keep our HTTP client options working, and reuse connections without mixing up targets. If a socket is down, the scrape should fail rather than fall back to TCP.

Targets come and go; release socket-specific client state when its last target disappears, without disrupting the remaining targets.

Do not look for or use online solutions.
