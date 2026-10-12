# swe-environment-lab

Seven PR-derived software-engineering tasks from **Prometheus, Thanos and VictoriaMetrics**, built to evaluate coding agents against running systems. Each task starts from a pinned repository snapshot and provides a working development environment, a maintainer-style request and a separate behavioral verifier.

I built this with an AI coding agent. I set the requirements, challenged its conclusions and directed the reviews; the agent implemented the environments and graders, ran calibration, and inspected the submitted solutions. The main question I kept pushing on was whether we were measuring the engineering work the original change required, or just whether a patch could pass the tests we had written.

## What I wanted the evaluation to capture

I wanted each task to include a live deployment the agent could interact with. That would let it reproduce the problem, change the code and observe the effect in a running system. The environment needed to support that engineering workflow, including failures that only appear when services interact or state survives a restart.

That requirement led to a full Thanos query path with Prometheus, Sidecar, Query, Query Frontend and Memcached. The services use actual storage and query paths, with sample traffic to make the deployment useful. I later pushed back on the extra tutorials and ready-made storage reproducers the agent had added. I wanted engineers to investigate the repository and running system themselves, so those additions were removed while the deployment and its controls stayed.

I also wanted the request to sound like a teammate asking a senior engineer to do the work. Reading [Snorkel's Senior SWE-Bench methodology](https://senior-swe-bench.snorkel.ai/blog/2026-06-16-how-it-works) helped me articulate that: give the agent the problem and intended behavior, then leave room for it to investigate the codebase and choose a design. I asked for obvious setup directions and evaluation boilerplate to be removed from the prompts.

That did not mean leaving the acceptance criteria ambiguous. The agent's shortening dropped standalone subqueries and entirely missing series from two requests, even though the graders expected them. Those requirements went back in before the affected tasks were rerun. The distinction I wanted was between existing repository conventions an engineer should discover and requirements they would otherwise have to guess. A later audit found the agent had gone too far in the other direction: it supplied the finished timestamp encoding as an “agreed format” and enforced an author-chosen storage limit. Those requirements were removed together with their grader checks. Existing formats and APIs still matter; a new design does not have to match the reference merely because the reference chose it.

## Why a passing solution needed another look

I wanted to know whether a passing patch covered the engineering concerns behind the upstream change. When the agent described three passing Unix-socket solutions as “differing implementations,” I asked what actually differed: what had the PR author accounted for, what had the alternatives missed, and could an alternative be better?

That question changed the review from comparing scores to comparing behavior. Local diagnostic replays found that all three solutions overrode a caller-supplied dial option and retained removed clients. The agent added checks through the repository's existing scraper API for those omissions. A repaired alternative kept its design and passed the targeted compatibility check. Different code was not sufficient reason to reject it; broken compatibility was.

The same comparison also found a limit in the reference. One earlier-cohort WAL alternative handled a repeated-restart boundary better but retained stale metadata longer. Because the reference itself failed that restart case, we did not add it as a grading gate. The experiment established a tradeoff, not a universally better implementation.

I asked for the graders themselves to be checked because a low solve rate could otherwise reflect mistakes in the evaluation. Thanos made that concern concrete. An earlier passing solution fetched compatible cache keys sequentially, missing a batching concern discussed upstream. The agent's first probe counted keys rather than remote commands and rejected the reference. That measurement had to be corrected before it could say anything useful about a candidate.

That mattered again in the final run. A Thanos submission passed all 30 checks and returned the right values, but reviewing its code showed it replaced old cached coverage with only the newly fetched part. Repeating an expanded request kept fetching data again. We added float and histogram checks for that existing behavior: the original PR passed both; the unchanged agent submission failed both. Its initial pass and corrected result are recorded separately. A changed grader should not look like a change in model ability.

## What the recorded runs showed

The final October 12 run used **one GPT-6 Luna attempt per task: 1/7 passed** under the corrected graders. The initial score was 2/7 before the Thanos false positive was caught. These seven agents ran with the cleaned environments and no added task guides. This is a small sample for inspecting behavior, not a model ranking.

| Task | Source PRs | Fresh run findings |
|---|---|---|
| [Unix-socket scraping](tasks/unix-socket-scraping/)<br>Collect metrics over Unix sockets while preserving HTTP/TLS behavior. | Prometheus [18091](https://github.com/prometheus/prometheus/pull/18091), [19399](https://github.com/prometheus/prometheus/pull/19399) | Failed caller-option precedence; routing, reload and retirement checks passed. |
| [Fixed-time subqueries](tasks/prometheus-subquery-semantics/)<br>Keep nested queries pinned to a fixed time correct across evaluation paths. | Prometheus [19187](https://github.com/prometheus/prometheus/pull/19187) | Fixed function arguments but still failed a standalone computed subquery. |
| [WAL expiry](tasks/stale-wal-expiry/)<br>Retain deleted-series metadata until log replay no longer needs it. | Prometheus [18847](https://github.com/prometheus/prometheus/pull/18847) | Passed: fixed timestamp expiry and added tombstone cleanup. |
| [Histogram rates](tasks/histogram-rate-semantics/)<br>Calculate histogram increases and rates correctly across resets and range boundaries. | Prometheus [18564](https://github.com/prometheus/prometheus/pull/18564), [18943](https://github.com/prometheus/prometheus/pull/18943) | Passed the numerical comparisons, then missed the warning for incompatible histogram schemas. |
| [Start-timestamp persistence](tasks/start-timestamp-persistence/)<br>Preserve histogram start timestamps through restarts and storage compaction. | Prometheus [18609](https://github.com/prometheus/prometheus/pull/18609) | Implemented an encoding but omitted its flag from WAL replay, losing timestamps after restart. |
| [Query-cache reuse](tasks/thanos-cache-reuse/)<br>Reuse cached query results at different time intervals without losing samples. | Thanos [8876](https://github.com/thanos-io/thanos/pull/8876) | Initially 30/30; corrected to 30/32 after detecting discarded cache coverage. |
| [Missing-series fill](tasks/victoriametrics-fill-modifiers/)<br>Apply fill values when vector operations encounter missing samples or series. | VictoriaMetrics [11207](https://github.com/VictoriaMetrics/VictoriaMetrics/pull/11207) | 90/135 checks; lost whole series and mishandled scalar and label behavior. |

I asked for completed trajectories to be reviewed without intervening in the solvers' work. That let us separate specific omissions from broader failures. The timestamp implementation missed replay configuration; the histogram implementation reached the numerical checks before missing an established warning. Neither establishes that one small edit would make the whole submission pass. VictoriaMetrics had broader gaps, including dropping entire series.

Across these seven trajectories, we found no visible online solution lookup or hidden-grader access. The agents investigated the source and, in several cases, ran repository tests; none directly used the supplied live deployment. The services were available and independently checked, but providing them did not make the agents choose that workflow. Harbor's automated reviews were checked against the submitted code and execution evidence before using their conclusions.

## Run the tasks

The table above summarizes the results. [results.json](results.json) keeps the outcomes, findings and run identifiers in one small file; the full run logs stay local.

The self-contained Harbor packages are in [tasks/](tasks/), including their source archives, environments, reference patches and tests. Some tasks combine related PRs into one request. [NOTICE](NOTICE) records the source and reference commits.

Install the pinned runner and set your Daytona token, then run a reference solution:

```sh
python3 -m pip install -r requirements.txt
export DAYTONA_API_KEY="your-token"
harbor run -e daytona -p tasks/unix-socket-scraping -a oracle \
  --override-cpus 4 --override-memory-mb 8192 --delete
```

Use `-a nop` for the unchanged baseline or a Harbor-supported agent for solver runs. Project-authored files use [Apache-2.0](LICENSE); upstream source retains its [licenses and notices](NOTICE).
