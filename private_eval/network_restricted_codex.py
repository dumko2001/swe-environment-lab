"""Explicit author-side Codex web-only and Daytona network-restricted runners.

This limits direct external retrieval. It does not isolate model credentials from
actor code or prove allowed model endpoints cannot perform indirect retrieval.
"""
from __future__ import annotations

from datetime import datetime, timezone
import ipaddress
import json
import re
import shlex

from harbor.agents.installed.codex import Codex
from harbor.environments.daytona.environment import DaytonaEnvironment


def normalize_domains(value: str | list[str]) -> list[str]:
    items = value.split(',') if isinstance(value, str) else value
    if not isinstance(items, list) or not items:
        raise ValueError('Explicit nonempty allowed_domains is required')
    result = []
    for item in items:
        domain = str(item).strip().lower().rstrip('.')
        if not re.fullmatch(r'(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', domain):
            raise ValueError('Allowlist requires literal DNS hostnames without wildcard, URL or port')
        try:
            ipaddress.ip_address(domain)
        except ValueError:
            pass
        else:
            raise ValueError('IP addresses are not domain allowlist entries')
        if domain not in result:
            result.append(domain)
    return sorted(result)


class WebDisabledCodex(Codex):
    """Disable the normal Codex web tool, without claiming network isolation.

    Select this class explicitly when public shell egress is acceptable and
    separately audited. It is never an automatic network-policy fallback.
    """
    def __init__(self, *args, **kwargs):
        self._web_disabled_environment = None
        super().__init__(*args, **kwargs)

    def build_cli_flags(self):
        # Command-line override wins over an actor-writable config file.
        return super().build_cli_flags() + ' -c ' + shlex.quote('web_search="disabled"')

    def _web_receipt(self, **details):
        self.logs_dir.mkdir(parents=True, exist_ok=True)
        payload = {'at': datetime.now(timezone.utc).isoformat(),
                   'scope': 'normal Codex web tool only; shell/API egress is not restricted by this class',
                   **details}
        (self.logs_dir / 'web-policy.json').write_text(json.dumps(payload, indent=2) + '\n')

    async def setup(self, environment):
        requested_version = self._version
        self._web_disabled_environment = None
        await super().setup(environment)
        prefix = 'if [ -s ~/.nvm/nvm.sh ]; then . ~/.nvm/nvm.sh; fi; '
        try:
            # Both checks are local CLI config parsing, not inference calls.
            positive = await self.exec_as_agent(environment, command=prefix +
                "codex -c 'web_search=\"disabled\"' features list >/dev/null 2>&1")
            negative = await self.exec_as_agent(environment, command=prefix +
                "if codex -c 'web_search=\"network-policy-invalid-mode\"' features list >/dev/null 2>&1; then exit 1; else exit 0; fi")
            if positive.return_code != 0 or negative.return_code != 0:
                raise RuntimeError('Installed Codex cannot validate explicit web_search=disabled')
            version = await self.exec_as_agent(environment, command=prefix + 'codex --version')
            actual_version = self.parse_version(version.stdout or '')
            if version.return_code != 0 or not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:[.+-][A-Za-z0-9.-]+)?', actual_version):
                raise RuntimeError('Could not verify the installed Codex version')
            if requested_version is not None and actual_version != requested_version:
                raise RuntimeError('Installed Codex version differs from requested version')
            self._version = actual_version
            self._web_disabled_environment = environment
            self._web_receipt(status='applied', web_search='disabled',
                              requested_version=requested_version, codex_version=actual_version)
        except Exception as error:
            self._web_receipt(status='failed', error_type=type(error).__name__)
            raise RuntimeError('Required Codex web configuration/version check failed; Codex was not started') from None

    async def run(self, instruction, environment, context):
        if self._web_disabled_environment is not environment:
            raise RuntimeError('Codex web configuration was not validated for this environment')
        return await super().run(instruction, environment, context)


class NetworkRestrictedCodex(WebDisabledCodex):
    def __init__(self, *args, allowed_domains, **kwargs):
        self.allowed_domains = normalize_domains(allowed_domains)
        self._restricted_sandbox_id = None
        super().__init__(*args, **kwargs)

    def _receipt(self, **details):
        self.logs_dir.mkdir(parents=True, exist_ok=True)
        payload = {'at': datetime.now(timezone.utc).isoformat(),
                   'requested_domains': self.allowed_domains, **details}
        # self.logs_dir is author-side, never uploaded into the actor filesystem.
        (self.logs_dir / 'network-policy.json').write_text(json.dumps(payload, indent=2) + '\n')

    async def setup(self, environment):
        self._restricted_sandbox_id = None
        if not isinstance(environment, DaytonaEnvironment) or getattr(environment, '_compose_mode', False):
            raise RuntimeError('Restricted Codex requires a direct Daytona actor sandbox')
        await super().setup(environment)
        sandbox = getattr(environment, '_sandbox', None)
        if sandbox is None or not callable(getattr(sandbox, 'update_network_settings', None)):
            self._receipt(status='failed', reason='sandbox policy API unavailable')
            raise RuntimeError('Cannot apply the required Daytona domain restriction')
        try:
            await sandbox.update_network_settings(domain_allow_list=','.join(self.allowed_domains))
            await sandbox.refresh_data()
            actual = normalize_domains(getattr(sandbox, 'domain_allow_list', None) or [])
            if actual != self.allowed_domains:
                raise RuntimeError('Daytona policy readback differs from requested domains')
            if getattr(sandbox, 'network_allow_list', None):
                raise RuntimeError('Unexpected CIDR exception on restricted actor sandbox')
            if getattr(sandbox, 'network_block_all', None):
                raise RuntimeError('Daytona reports block-all instead of the requested model-domain policy')
            self._restricted_sandbox_id = sandbox.id
            self._receipt(status='applied', sandbox_id=sandbox.id, readback_domains=actual,
                          codex_version=self._version, web_search='disabled',
                          scope='direct domain egress; allowed model endpoints are not payload-restricted')
        except Exception as error:
            self._receipt(status='failed', sandbox_id=sandbox.id, error_type=type(error).__name__)
            # SDK exception bodies can contain request details: keep the public
            # error and policy receipt free of raw headers or credential values.
            raise RuntimeError('Required actor network policy/configuration failed; Codex was not started') from None

    async def run(self, instruction, environment, context):
        sandbox = getattr(environment, '_sandbox', None)
        if sandbox is None or sandbox.id != self._restricted_sandbox_id:
            raise RuntimeError('Actor sandbox was not prepared with the required network policy')
        try:
            await sandbox.refresh_data()
            actual = normalize_domains(getattr(sandbox, 'domain_allow_list', None) or [])
            if actual != self.allowed_domains or getattr(sandbox, 'network_allow_list', None) or getattr(sandbox, 'network_block_all', None):
                raise RuntimeError('Actor network policy changed before execution')
        except Exception:
            self._receipt(status='failed', sandbox_id=sandbox.id, reason='pre-run policy check failed')
            raise RuntimeError('Actor network policy could not be confirmed; Codex was not started') from None
        return await super().run(instruction, environment, context)
