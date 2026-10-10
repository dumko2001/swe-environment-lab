#!/usr/bin/env python3
"""Materialize or verify the complete Prometheus Harbor task suite."""

from __future__ import annotations

import argparse
import hashlib
import shutil
import tomllib
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
ENVIRONMENT = ROOT / "task_support/prometheus/environment"
TASKS = {
    "unix-socket-scraping": "unix-socket-scraping",
    "step-invariant-subquery": "prometheus-subquery-semantics",
    "native-histogram-rate": "histogram-rate-semantics",
    "start-timestamp-persistence": "start-timestamp-persistence",
    "stale-series-wal-expiry": "stale-wal-expiry",
}
SUITE = tomllib.loads((ROOT / "task_support/prometheus/suite.toml").read_text())
REGISTRY = {entry["slug"]: entry for entry in SUITE["tasks"]}
AUTHOR_ONLY_DIRS = {"calibration", "fixtures"}
ENVIRONMENT_EXCLUDES = {
    Path("README.md"),
    Path("profiles/README.md"),
}
TASK_ENVIRONMENT_EXCLUDES = {
    "native-histogram-rate": {Path("runtime/exporters.py")},
}


def include_environment_file(path: Path) -> bool:
    relative = path.relative_to(ENVIRONMENT)
    return relative not in ENVIRONMENT_EXCLUDES and relative.parts[0] != "verifier"


def file_hash(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_tree(root: Path) -> None:
    for path in root.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError(f"unsafe filesystem entry: {path}")


def validate_snapshot(task_name: str, overlay: Path) -> None:
    entry = REGISTRY[task_name]
    source = overlay / "source"
    archive = source / "prometheus-source.tar.gz"
    if not archive.is_file():
        raise ValueError(f"missing registered source archive: {archive}")
    observed = file_hash(archive)
    if observed != entry["source_sha256"]:
        raise ValueError(
            f"source hash mismatch for {task_name}: {observed} != {entry['source_sha256']}"
        )


def expected_files(task_name: str, overlay: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    for base, prefix in ((ENVIRONMENT, "environment"), (overlay, "")):
        for path in sorted(base.rglob("*")):
            if not path.is_file():
                continue
            if base == ENVIRONMENT and not include_environment_file(path):
                continue
            if base == ENVIRONMENT and path.relative_to(base) in TASK_ENVIRONMENT_EXCLUDES.get(task_name, set()):
                continue
            relative = Path(prefix) / path.relative_to(base)
            if base == overlay and relative.parts[0] in AUTHOR_ONLY_DIRS:
                continue
            if prefix == "" and relative.parts[0] == "source":
                if relative.name != "SOURCE.md":
                    result[(Path("environment") / relative).as_posix()] = file_hash(path)
                if relative.name.endswith(".tar.gz"):
                    result[(Path("tests") / relative).as_posix()] = file_hash(path)
            else:
                result[relative.as_posix()] = file_hash(path)
    for path in sorted((ENVIRONMENT / "verifier").rglob("*")):
        if path.is_file():
            verifier_relative = path.relative_to(ENVIRONMENT / "verifier")
            result.setdefault(
                (Path("tests/common") / verifier_relative).as_posix(),
                file_hash(path),
            )
    if task_name == "start-timestamp-persistence":
        result["tests/oracle.patch"] = file_hash(overlay / "solution/fix.patch")
    return result


def materialize(task_name: str, overlay: Path, destination: Path) -> None:
    validate_tree(ENVIRONMENT)
    validate_tree(overlay)
    validate_snapshot(task_name, overlay)
    temporary = destination.parent / f".{destination.name}.tmp"
    backup = destination.parent / f".{destination.name}.old"
    for transient in (temporary, backup):
        if transient.exists():
            shutil.rmtree(transient)
    shutil.copytree(
        ENVIRONMENT,
        temporary / "environment",
        ignore=lambda directory, names: [
            name
            for name in names
            if not include_environment_file(Path(directory) / name)
        ],
    )
    verifier_common = ENVIRONMENT / "verifier"
    if verifier_common.is_dir():
        shutil.copytree(verifier_common, temporary / "tests/common")
    for path in sorted(overlay.rglob("*")):
        if not path.is_file():
            continue
        relative = path.relative_to(overlay)
        if relative.parts[0] in AUTHOR_ONLY_DIRS:
            continue
        if relative.parts[0] == "source":
            targets = []
            if relative.name != "SOURCE.md":
                targets.append(temporary / "environment" / relative)
            if relative.name.endswith(".tar.gz"):
                targets.append(temporary / "tests" / relative)
        else:
            targets = [temporary / relative]
        for target in targets:
            target.parent.mkdir(parents=True, exist_ok=True)
            if target.exists():
                target.chmod(0o644)
            shutil.copy2(path, target)
    if task_name == "start-timestamp-persistence":
        shutil.copy2(
            overlay / "solution/fix.patch",
            temporary / "tests/oracle.patch",
        )
    for relative in TASK_ENVIRONMENT_EXCLUDES.get(task_name, set()):
        (temporary / "environment" / relative).unlink(missing_ok=True)
    if destination.exists():
        destination.rename(backup)
    try:
        temporary.rename(destination)
    except Exception:
        if backup.exists() and not destination.exists():
            backup.rename(destination)
        raise
    if backup.exists():
        shutil.rmtree(backup)


def verify(task_name: str, overlay: Path, destination: Path) -> int:
    validate_tree(ENVIRONMENT)
    validate_tree(overlay)
    validate_snapshot(task_name, overlay)
    expected = expected_files(task_name, overlay)
    actual = {
        path.relative_to(destination).as_posix(): file_hash(path)
        for path in sorted(destination.rglob("*"))
        if path.is_file()
    } if destination.exists() else {}
    missing = sorted(expected.keys() - actual.keys())
    unexpected = sorted(actual.keys() - expected.keys())
    changed = sorted(name for name in expected.keys() & actual.keys() if expected[name] != actual[name])
    if missing or unexpected or changed:
        if missing:
            print("missing=" + ",".join(missing))
        if unexpected:
            print("unexpected=" + ",".join(unexpected))
        if changed:
            print("changed=" + ",".join(changed))
        return 1
    print(f"verified {len(expected)} files: {destination}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--verify", action="store_true")
    parser.add_argument(
        "--task",
        action="append",
        choices=sorted(TASKS),
        help="materialize one task; repeat for several (default: all)",
    )
    args = parser.parse_args()
    selected = args.task or list(TASKS)
    status = 0
    for task_name in selected:
        overlay = ROOT / "task_support/prometheus/tasks" / task_name
        destination = ROOT / "tasks" / TASKS[task_name]
        if not overlay.is_dir():
            print(f"missing overlay: {overlay}")
            status = 1
            continue
        if not args.verify:
            materialize(task_name, overlay, destination)
        status = max(status, verify(task_name, overlay, destination))
    return status


if __name__ == "__main__":
    raise SystemExit(main())
