# swe-environment-lab

Seven PR-derived software-engineering tasks from **Prometheus, Thanos and VictoriaMetrics**, built to evaluate coding agents against running systems. Each task starts from a pinned repository snapshot and provides a working development environment, a maintainer-style request and a separate behavioral verifier.

I built this with an AI coding agent. I set the requirements, challenged its conclusions and directed the reviews; the agent implemented the environments and graders, ran calibration, and inspected the submitted solutions. The main question I kept pushing on was whether we were measuring the engineering work the original change required, or just whether a patch could pass the tests we had written.

## What I wanted the evaluation to capture

I wanted each task to include a live deployment the agent could interact with. That would let it reproduce the problem, change the code and observe the effect in a running system. The environment needed to support that engineering workflow, including failures that only appear when services interact or state survives a restart.

That requirement led to a full Thanos query path with Prometheus, Sidecar, Query, Query Frontend and Memcached. For storage tasks, the agent added drivers that exercise actual checkpoints, replay and block reopen. A successful HTTP response cannot tell us whether a histogram's start timestamp survives a restart. Controlled inputs help reproduce those cases; the storage and query components themselves need to be real.

I also wanted the request to sound like a teammate asking a senior engineer to do the work. Reading [Snorkel's Senior SWE-Bench methodology](https://senior-swe-bench.snorkel.ai/blog/2026-06-16-how-it-works) helped me articulate that: give the agent the problem and intended behavior, then leave room for it to investigate the codebase and choose a design. I asked for obvious setup directions and evaluation boilerplate to be removed from the prompts.

That did not mean leaving the acceptance criteria ambiguous. The agent's shortening dropped standalone subqueries and entirely missing series from two requests, even though the graders expected them. Those requirements went back in before the affected tasks were rerun. The distinction I wanted was between details an engineer can discover in the repository and requirements they would otherwise have to guess.

## Why a passing solution needed another look

I wanted to know whether a passing patch covered the engineering concerns behind the upstream change. When the agent described three passing Unix-socket solutions as “differing implementations,” I asked what actually differed: what had the PR author accounted for, what had the alternatives missed, and could an alternative be better?

That question changed the review from comparing scores to comparing behavior. Local diagnostic replays found that all three solutions overrode a caller-supplied dial option and retained removed clients. The agent added checks for those omissions without requiring the upstream client's internal organization. A repaired alternative kept its design and passed the targeted compatibility check. Different code was not sufficient reason to reject it; broken compatibility was.

The same comparison also found a limit in the reference. One earlier-cohort WAL alternative handled a repeated-restart boundary better but retained stale metadata longer. Because the reference itself failed that restart case, we did not add it as a grading gate. The experiment established a tradeoff, not a universally better implementation.

I asked for the graders themselves to be checked because a low solve rate could otherwise reflect mistakes in the evaluation. Thanos made that concern concrete. An earlier passing solution fetched compatible cache keys sequentially, missing a batching concern discussed upstream. The agent's first probe counted keys rather than remote commands and rejected the reference. That measurement had to be corrected before it could say anything useful about a candidate.

A later passing Thanos submission omitted a lone final timestamp. The agent added float and histogram cases, checked them against the reference, unchanged code and archived submission, then ran fresh attempts. We kept results attached to the verifier version that produced them. Otherwise, tightening a grader could look like a change in model ability when the measurement had changed.

## What the final runs showed

The final selection contains two GPT-6 Luna attempts per task: **1/14 passed**. That is a small calibration sample, not a model ranking or proof that low solve rate means high task quality. The packages changed during review; superseded runs are excluded from this selection.

| Task | Source PRs | Final trajectory findings |
|---|---|---|
| [Unix-socket scraping](tasks/unix-socket-scraping/)<br>Collect metrics over Unix sockets while preserving HTTP/TLS behavior. | Prometheus [18091](https://github.com/prometheus/prometheus/pull/18091), [19399](https://github.com/prometheus/prometheus/pull/19399) | Both failed caller-option precedence despite passing routing, reload and retirement checks. |
| [Fixed-time subqueries](tasks/prometheus-subquery-semantics/)<br>Keep nested queries pinned to a fixed time correct across evaluation paths. | Prometheus [19187](https://github.com/prometheus/prometheus/pull/19187) | One returned 99 instead of 36; the other missed standalone evaluation. |
| [WAL expiry](tasks/stale-wal-expiry/)<br>Retain deleted-series metadata until log replay no longer needs it. | Prometheus [18847](https://github.com/prometheus/prometheus/pull/18847) | One passed. The other changed checkpoint filtering and weakened existing tests; the independent verifier rejected it. |
| [Histogram rates](tasks/histogram-rate-semantics/)<br>Calculate histogram increases and rates correctly across resets and range boundaries. | Prometheus [18564](https://github.com/prometheus/prometheus/pull/18564), [18943](https://github.com/prometheus/prometheus/pull/18943) | One omitted the initial range sample; the other missed a terminal reset, returning −90 instead of 372. |
| [Start-timestamp persistence](tasks/start-timestamp-persistence/)<br>Preserve histogram start timestamps through restarts and storage compaction. | Prometheus [18609](https://github.com/prometheus/prometheus/pull/18609) | One had chunk-lifecycle and format defects; the other missed replay wiring. |
| [Query-cache reuse](tasks/thanos-cache-reuse/)<br>Reuse cached query results at different time intervals without losing samples. | Thanos [8876](https://github.com/thanos-io/thanos/pull/8876) | 9/30 and 27/30 checks. The latter lost fetched points during merging and advertised cache coverage beyond its stored data. |
| [Missing-series fill](tasks/victoriametrics-fill-modifiers/)<br>Apply fill values when vector operations encounter missing samples or series. | VictoriaMetrics [11207](https://github.com/VictoriaMetrics/VictoriaMetrics/pull/11207) | One failed compilation; the other passed 111/135 checks but mishandled missing series and metric names. |

I asked the agent to read through the completed runs to understand what went wrong and check for shortcuts. Some fixes were small: adding two missing settings fixed one restart test. But we had not run that repaired version through every test, so it still could not count as a pass. The Thanos solution that passed 27 of 30 checks was a different case: it still left data out of query results.

We found no signs of agents looking up the answers or accessing the hidden tests, though internet downloads were allowed. One WAL attempt changed existing tests to fit its broken code; our separate tests still caught the bug. Harbor's review tool also got some facts and counts wrong, so the agent checked those reviews against the code and actual test output.

## Run the tasks

The table above summarizes the results. [results.json](results.json) keeps the recorded test outputs and identifiers in one small file; the full run logs stay local.

The runnable packages are in [tasks/](tasks/). [task_support/](task_support/) contains the pinned source, reference patches and files used to build them. Some tasks combine related PRs into one request.

Copy `.env.example` to `.env` and set `DAYTONA_API_KEY`, then run a reference solution with the live-service checks:

```sh
# With Harbor 0.14.0 and Daytona configured:
python3 tools/run_eval.py \
  --task tasks/unix-socket-scraping --agent oracle-smoke \
  --job live-env-swe-lab-uds-oracle-01 --env-file .env
```

The runner also supports `nop-smoke` for the unchanged baseline and `codex` for solver trials. Project-authored files use [Apache-2.0](LICENSE); upstream source retains its [licenses and notices](NOTICE).
