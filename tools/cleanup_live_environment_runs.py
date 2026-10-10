#!/usr/bin/env python3
"""Audit/delete only completed explicitly named local Harbor jobs' sandboxes."""
import argparse,hashlib,json,os,time
from pathlib import Path
from daytona import Daytona,ListSandboxesQuery
p=argparse.ArgumentParser();p.add_argument('--job',action='append',required=True);p.add_argument('--env-file',type=Path,required=True);p.add_argument('--delete',action='store_true');p.add_argument('--receipt',type=Path,required=True);a=p.parse_args()
for line in a.env_file.read_text().splitlines():
    k,v=line.split('=',1)
    if k.startswith('DAYTONA_'):os.environ[k]=v
client=Daytona();rows=[]
def list_owned(labels):
    # Provider listing occasionally returns transient gateway/rate-limit errors.
    # Retry only reads; preserve ownership checks before any deletion.
    for attempt in range(4):
        try:
            return list(client.list(ListSandboxesQuery(labels=labels)))
        except Exception as exc:
            if attempt == 3 or not any(token in str(exc).lower() for token in ('502', '503', '504', '429', 'bad gateway', 'timed out')):
                raise
            time.sleep(2 ** attempt)

for job in a.job:
    if '/' in job or not job.startswith('live-env-'):raise ValueError('Expected an exact live-env job name')
    root=Path('jobs')/job
    result=json.loads((root/'result.json').read_text())
    if a.delete and not result.get('finished_at'):raise RuntimeError('Job is still active: '+job)
    trials={json.loads(p.read_text())['trial_name'] for p in root.glob('*/config.json')}
    sessions=set(trials)
    for trial in trials:
        raw=trial+'__verifier__trial'
        if len(raw)>63:
            suffix='__'+hashlib.sha1(raw.encode()).hexdigest()[:8]
            raw=raw[:63-len(suffix)].rstrip('-._')+suffix
        sessions.add(raw)
    found={}
    for sandbox in list_owned({'micro1.run':job}):found[sandbox.id]=sandbox
    for trial in sessions:
        for sandbox in list_owned({'harbor.session_id':trial}):found[sandbox.id]=sandbox
    for sandbox in found.values():
        labels=sandbox.labels
        if labels.get('harbor.managed')!='true' or labels.get('harbor.session_id') not in sessions:
            raise RuntimeError('Sandbox ownership not proven for '+sandbox.id)
        row={'job':job,'sandbox_id':sandbox.id,'labels':labels,'state':str(sandbox.state),'action':'observed'}
        if a.delete:
            client.delete(sandbox);row['action']='deleted'
        rows.append(row)
    rows.append({'job':job,'matched_sandboxes':len(found),'cleanup_requested':a.delete})
a.receipt.parent.mkdir(parents=True,exist_ok=True);a.receipt.write_text(json.dumps(rows,indent=2)+'\n')
print(json.dumps(rows,indent=2))
