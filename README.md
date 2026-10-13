# swe-environment-lab
Seven SWE environments built from Prometheus, Thanos and VictoriaMetrics issues and PRs, packaged in Harbor.
I made the first version with AI. Then I started reviewing what it had built: the tasks, the graders, and the agents’ attempts. That’s where I found the problems.

## What I changed and learned
I used the upstream PR as the reference solution. I wanted the tasks to feel like work you’d give a senior engineer: describe the problem, let them investigate the repo and choose how to solve it. They also have a running system they can test against.
The graders needed to allow other valid solutions. For one task, the agent had required its chosen storage format. I removed that requirement and its checks. What mattered was whether the data survived correctly.
I also asked the agent to review passing solutions. In earlier runs, three Unix-socket solutions passed, but all ignored caller settings and kept clients that should have been removed. I asked the agent to add checks for those problems. A repaired alternative passed those checks without copying the reference’s design.
The grader could be wrong too. One Thanos check counted cache keys instead of actual requests and failed the reference solution. I had the agent fix the measurement.
Later, a Thanos solution passed all 30 checks but discarded previously cached data when fetching more. Two new checks caught it. The reference passed both; the same agent solution failed both.
Reviewing the trajectories helped explain failures too. Two attempts could get the same score for different reasons. In the latest Thanos runs, one made no changes; the other changed code that the active frontend wasn’t using.

## Current results
This submission includes seven SWE environments and **14 GPT-6 Luna attempts**, two per task. One WAL-expiry attempt passed; every other attempt failed. This is a small calibration sample.

- [Tasks](tasks/): environments, reference solutions and graders
- [Results](results.json): scores and findings for each attempt
- [Trajectories](jobs/): all 14 agent trajectories and run results
- [NOTICE](NOTICE): source repositories and PRs

Detailed verifier logs and earlier runs aren’t included in this ZIP.

## Run
Requires Python 3.12+ and a Daytona token.

```sh
python3 -m pip install -r requirements.txt
export DAYTONA_API_KEY="your-token"
harbor run -e daytona -p tasks/unix-socket-scraping -a oracle \
  --override-cpus 4 --override-memory-mb 8192 --delete
```

Use `-a nop` to test the unchanged starting code.
Project files: [Apache-2.0](LICENSE). Upstream code keeps its [licenses](NOTICE).
