"""Unchanged reference solution with independent public runtime lifecycle checks."""
from harbor.agents.oracle import OracleAgent
from harbor.models.trial.paths import TrialPaths
from pathlib import Path
import re

class LiveEnvironmentOracle(OracleAgent):
    def __init__(self,logs_dir,task_dir,apply_oracle=True,**kwargs):
        super().__init__(logs_dir=logs_dir,task_dir=Path(task_dir),trial_paths=TrialPaths(trial_dir=logs_dir.parent),**kwargs)
        self.apply_oracle=apply_oracle
    async def check(self,environment,phase,commands):
        for index,command in enumerate(commands):
            result=await environment.exec(command=command,user='agent',timeout_sec=1200)
            (self.logs_dir/f'live-{phase}-{index}.txt').write_text(f'$ {command}\nexit={result.return_code}\n{result.stdout}\n{result.stderr}\n')
            if result.return_code:
                raise RuntimeError(f'Public environment check failed: {phase} {command}')
    async def run(self,instruction,environment,context):
        task=self._task.task_dir.name
        await environment.upload_file(source_path=Path(__file__).resolve().parents[1]/'tools/audit_live_actor.py',target_path='/tmp/audit_live_actor.py')
        await self.check(environment,'visibility',['python3 /tmp/audit_live_actor.py'])
        await environment.upload_file(source_path=Path(__file__).resolve().parents[1]/'tools/live_environment_smoke.py',target_path='/tmp/live_environment_smoke.py')
        smoke=f'python3 /tmp/live_environment_smoke.py {task}'
        await self.check(environment,'baseline',["bash -lc 'command -v go && command -v gofmt && go version'",'task-env status',smoke+' --phase baseline --mode capture','task-env restart',smoke+' --phase baseline --mode compare'])
        if self.apply_oracle:
            await super().run(instruction,environment,context)
        phase='oracle' if self.apply_oracle else 'baseline'
        await self.check(environment,'rebuilt',["bash -lc 'task-env rebuild'",smoke+f' --phase {phase} --mode compare'])
        if task in ('start-timestamp-persistence','stale-wal-expiry'):
            await self.check(environment,'storage',['task-env storage-demo'])
        if task=='start-timestamp-persistence' and self.apply_oracle:
            await self.check(environment,'storage-enabled',['STORAGE_FEATURES=histograms-st-encoding,xor2-encoding task-env storage-demo'])
            body=(self.logs_dir/'live-storage-enabled-0.txt').read_text()
            starts=re.findall(r'\bstart=(\d+)\b',body)
            if len(starts)<6 or set(starts)!={'60000'}:
                raise RuntimeError('Enabled public storage demo did not preserve histogram start timestamps across its lifecycle')
        await self.check(environment,'reset',['task-env reset-data',smoke+f' --phase {phase} --mode check'])
