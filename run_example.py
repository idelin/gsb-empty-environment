"""End-to-end demonstration of the multi-output build cache.

Runs entirely offline against demo_manifest.json with the Python standard
library only:

    python3.11 run_example.py
"""

from __future__ import annotations

import shutil
import sys
import tempfile
import threading
from pathlib import Path

from buildcache import (
    BuildCache,
    BuildCoordinator,
    BuildStatus,
    MissingOutputError,
    NonDeterministicConflict,
    PublishResult,
    build_key,
    load_manifest,
)


def hr(title: str) -> None:
    print(f"\n=== {title} ===")


def make_stage(root: Path, name: str, files: dict[str, bytes]) -> Path:
    stage = root / name
    if stage.exists():
        shutil.rmtree(stage)
    for filename, data in files.items():
        path = stage / filename
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
    return stage


def main() -> int:
    manifest = load_manifest(Path(__file__).parent / "demo_manifest.json")
    node = manifest.nodes["compile-core"]
    key = node.cache_key

    work = Path(tempfile.mkdtemp(prefix="bcache-demo-"))
    cache_root = work / "cache"
    cache = BuildCache(cache_root)
    coord = BuildCoordinator(cache)
    print(f"cache root : {cache_root}")
    print(f"node       : {node.name} ({node.package} {node.version})")
    print(f"cache key  : {key[:16]}…")
    print(f"outputs    : {', '.join(node.outputs)}")

    hr("1. first build: both outputs present -> PUBLISHED")
    stage = make_stage(work, "s1", {node.outputs[0]: b"OBJECT-CODE-v1", node.outputs[1]: b"MAP-v1"})
    result = cache.publish(
        key, stage, package=node.package, version=node.version,
        inputs=node.inputs, outputs=node.outputs,
    )
    print(f"result     : {result.value}")
    print(f"verify     : {cache.verify_entry(key)}")

    hr("2. rebuild, identical artifacts -> DEDUPLICATED (nothing written)")
    stage = make_stage(work, "s2", {node.outputs[0]: b"OBJECT-CODE-v1", node.outputs[1]: b"MAP-v1"})
    result = cache.publish(
        key, stage, package=node.package, version=node.version,
        inputs=node.inputs, outputs=node.outputs, attempt="retry",
    )
    print(f"result     : {result.value}")

    hr("3. one output missing -> publish nothing")
    stage = make_stage(work, "s3", {node.outputs[0]: b"OBJECT-CODE-v1"})
    try:
        cache.publish(
            key, stage, package=node.package, version=node.version,
            inputs=node.inputs, outputs=node.outputs, attempt="partial",
        )
    except MissingOutputError as exc:
        print(f"refused    : {exc}")
    print(f"entries    : {cache.keys()} (existing entry untouched)")
    print(f"tmp leftover: {[p.name for p in cache.tmp_dir.iterdir()]}")

    hr("4. same key, different artifacts -> conflict, first writer wins")
    stage = make_stage(work, "s4", {node.outputs[0]: b"TAMPERED-OBJECT", node.outputs[1]: b"MAP-v1"})
    try:
        cache.publish(
            key, stage, package=node.package, version=node.version,
            inputs=node.inputs, outputs=node.outputs, attempt="nd",
        )
    except NonDeterministicConflict as exc:
        print(f"conflict   : {exc}")
    winner = (cache.entry_path(key) / node.outputs[0]).read_bytes().decode()
    print(f"kept       : {winner!r} (no overwrite)")

    hr("5. six concurrent duplicate builds -> coalesced into one execution")
    other = manifest.nodes["assemble-beta"]
    other_key = other.cache_key
    exec_count = 0
    count_lock = threading.Lock()
    outcomes: list[str] = []

    def builder(k, staging):
        nonlocal exec_count
        with count_lock:
            exec_count += 1
        threading.Event().wait(0.1)  # keep the flight open for late waiters
        (staging / other.outputs[0]).write_bytes(b"LIBBETA-FAT-A")

    def worker():
        outcome = coord.build(
            other_key, builder, package=other.package, version=other.version,
            inputs=other.inputs, outputs=other.outputs,
        )
        outcomes.append(outcome.status.value)

    threads = [threading.Thread(target=worker) for _ in range(6)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    print(f"executions : {exec_count}")
    print(f"outcomes   : {sorted(outcomes)}")

    hr("6. failed build cleans up; waiters fail too; retry succeeds")
    fail_key = build_key("core", "9.9.9", {"experiment": 1}, ["report.txt"])

    def failing_builder(k, staging):
        threading.Event().wait(0.05)
        (staging / "report.txt").write_bytes(b"partial")
        raise RuntimeError("compiler crashed")

    leader_error = []

    def fail_worker():
        try:
            coord.build(
                fail_key, failing_builder, package="core", version="9.9.9",
                inputs={"experiment": 1}, outputs=["report.txt", "symbols.txt"],
            )
        except RuntimeError as exc:
            leader_error.append(str(exc))

    threads = [threading.Thread(target=fail_worker) for _ in range(3)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    print(f"leader saw : {leader_error[0]!r}")
    print(f"entry exists: {cache.has(fail_key)}")
    print(f"tmp leftover: {[p.name for p in cache.tmp_dir.iterdir()]}")

    def good_builder(k, staging):
        (staging / "report.txt").write_bytes(b"done")
        (staging / "symbols.txt").write_bytes(b"S")

    retry = coord.build(
        fail_key, good_builder, package="core", version="9.9.9",
        inputs={"experiment": 1}, outputs=["report.txt", "symbols.txt"],
    )
    print(f"retry      : {retry.status.value}, verified={cache.verify_entry(fail_key)}")

    shutil.rmtree(work, ignore_errors=True)
    print("\nall scenarios completed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
