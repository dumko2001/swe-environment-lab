# Thanos Query Frontend cache reuse provenance

Upstream: https://github.com/thanos-io/thanos/pull/8876
Exact title: query-frontend: reuse lower-step cache entries
Live GitHub API verified 2026-09-06: merged=true.
Merge: bdba693318568b4e25078700983337ec03c7ada4
First parent (task source): b9f0bd038a7b9e7c8e4f2a3c96957d3e4d7251e4
Second parent / PR head: a63c8821bf916857a357c06f9a06ba055accae7f
PR-open base is different and is deliberately not used.

Archive: https://codeload.github.com/thanos-io/thanos/tar.gz/b9f0bd038a7b9e7c8e4f2a3c96957d3e4d7251e4
SHA256: 4e1d5b27d067735836484aff775207bfc3c8a2ed66d61ccc5be384f06a534927
License: Apache-2.0; original LICENSE and notices remain in archive.

The author-only patch is the merge-versus-first-parent production diff for
internal/cortex/querier/queryrange/results_cache.go, pkg/queryfrontend/cache.go,
and pkg/queryfrontend/response.go, plus associated cache regression tests.
Changelog and bundled UI
assets are omitted because the requested task contract is backend-only.
Source archive bytes in both task images match this archive.
