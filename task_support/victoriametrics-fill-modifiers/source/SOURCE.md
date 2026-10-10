# Source provenance (author only)

Upstream: https://github.com/VictoriaMetrics/VictoriaMetrics
PR: https://github.com/VictoriaMetrics/VictoriaMetrics/pull/11207
Landed commit: `fb26016beb0b708a254f78406712edd2df453c90`
Exact landed first parent: `66ac4114edc4871f5cc5f957c26b0332d9f9fd71`
Archive: https://codeload.github.com/VictoriaMetrics/VictoriaMetrics/tar.gz/66ac4114edc4871f5cc5f957c26b0332d9f9fd71
SHA256: `cf802f0aa8174af4898f315d014d9c89973af6061ba2617923b51921b4d2a91e`
Live GitHub API verification: 2026-09-06. Commit has exactly the recorded parent.

The full, unmodified upstream archive includes Apache-2.0 licensing and vendored dependency notices. The MetricsQL parser already supports the syntax in this parent (MetricsQL PR #62 landed first). The reference patch contains the two production changes and upstream regression tests from #11207, excluding unrelated changelog text. Neither the patch nor this provenance file enters the agent image.

Go image: `golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b` (Docker Registry manifest verified 2026-09-06).
