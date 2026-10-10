#!/usr/bin/env python3
"""Run an explicitly scoped Harbor/Daytona control or Luna trial without logging secrets."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys

p=argparse.ArgumentParser()
p.add_argument('--task',required=True)
p.add_argument('--agent',choices=['oracle','oracle-smoke','nop-smoke','nop','codex'],required=True)
p.add_argument('--job',required=True)
p.add_argument('--include',action='append',default=[])
p.add_argument('--attempts',type=int,default=1)
p.add_argument('--concurrency',type=int,default=1)
p.add_argument('--model',default='gpt-6-luna',help='Codex model; GPT-6 Luna was validated with the pinned CLI')
p.add_argument('--reasoning-effort',default='high',choices=['low','medium','high','xhigh','max'])
p.add_argument('--allowed-domain',action='append',default=[],help='Opt into verified Daytona model-domain restriction')
p.add_argument('--codex-version',default='0.162.1',help='Pin the installed actor CLI version')
p.add_argument('--disable-web-search',action=argparse.BooleanOptionalAction,default=True,help='Disable Codex web-search tool; does not block shell egress')
p.add_argument('--env-file',default='.env')
a=p.parse_args()
policy_path = Path(__file__).resolve().parents[1] / 'task_support/evaluation-policy.md'
policy = policy_path.read_text().strip()
if a.agent == 'codex' and not (Path(a.task) / 'instruction.md').read_text().rstrip().endswith(policy):
    raise SystemExit('Task must include the shared evaluation policy verbatim; refusing an unbound solver run')
if (Path('jobs')/a.job).exists():
    raise SystemExit('Job already exists; choose a new unique job name: '+a.job)
env=os.environ.copy()
for line in Path(a.env_file).read_text().splitlines():
    if line.strip() and not line.lstrip().startswith('#'):
        k,v=line.split('=',1)
        if k not in ('DAYTONA_API_KEY','DAYTONA_API_URL','DAYTONA_TARGET','CODEX_AUTH_JSON_PATH','CODEX_FORCE_AUTH_JSON'):
            raise SystemExit('Unexpected credential setting: '+k)
        env[k]=v
cmd=['harbor','run','-e','daytona','-p',a.task,'-k',str(a.attempts),'-n',str(a.concurrency),'--cpus','request','--memory','request','--override-cpus','4','--override-memory-mb','8192','--override-storage-mb','10240','--delete','--max-retries','1','--retry-include','DaytonaRateLimitError','--retry-include','DaytonaError','--yes','--job-name',a.job]
cmd+=['--ek', 'labels='+json.dumps({'micro1.run':a.job})]
for name in a.include:
    cmd+=['-i',name]
if a.agent in ('oracle-smoke','nop-smoke'):
    env['PYTHONPATH']=str(Path(__file__).resolve().parents[1])+os.pathsep+env.get('PYTHONPATH','')
    cmd+=['--agent-import-path','private_eval.live_environment_oracle:LiveEnvironmentOracle','--ak','task_dir='+str(Path(a.task).resolve())]
    if a.agent=='nop-smoke':
        cmd+=['--ak','apply_oracle=false']

else:
    if a.agent=='codex' and a.allowed_domain:
        env['PYTHONPATH']=str(Path(__file__).resolve().parents[1])+os.pathsep+env.get('PYTHONPATH','')
        cmd+=['--agent-import-path','private_eval.network_restricted_codex:NetworkRestrictedCodex','--ak','allowed_domains='+','.join(a.allowed_domain)]
    elif a.agent=='codex' and a.disable_web_search:
        env['PYTHONPATH']=str(Path(__file__).resolve().parents[1])+os.pathsep+env.get('PYTHONPATH','')
        cmd+=['--agent-import-path','private_eval.network_restricted_codex:WebDisabledCodex']
    else:
        cmd+=['-a',a.agent]
if a.agent=='codex':
    cmd+=['-m',a.model,'--ak','reasoning_effort='+a.reasoning_effort]
    if a.codex_version:
        cmd+=['--ak','version='+a.codex_version]
task=Path(a.task)
files={str(f.relative_to(task)):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(task.rglob('*')) if f.is_file() and '__pycache__' not in f.parts}
receipts=Path('tmp/run-inputs')
receipts.mkdir(parents=True,exist_ok=True)
(receipts/(a.job+'.json')).write_text(json.dumps({'task':str(task),'agent':a.agent,'model':a.model if a.agent=='codex' else None,'reasoning_effort':a.reasoning_effort if a.agent=='codex' else None,'allowed_domains':a.allowed_domain,'codex_version':a.codex_version if a.agent=='codex' else None,'web_search_disabled':(a.disable_web_search or bool(a.allowed_domain)) if a.agent=='codex' else None,'evaluation_policy':{'text':policy,'sha256':hashlib.sha256(policy_path.read_bytes()).hexdigest(),'enforcement':'trajectory review; not a claim of shell egress isolation'},'files':files},indent=2)+'\n')
os.execvpe(cmd[0],cmd,env)
