"""End-to-end sample for the multi-output build cache.

Run from the repository root:  python3.11 examples/demo.py
"""
from __future__ import annotations

import shutil
import sys
import tempfile
import threading
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from buildcache import (  # noqa: E402
    Builder,
    BuildCache,
    BuildError,
    BuildNode,
    NonDeterministicConflict,
    PackageRegistry,
)


def section(title):
    print(f"\n=== {title} ===")


def main():
    workspace = Path(tempfile.mkdtemp(prefix="buildcache-demo-"))
    print(f"workspace: {workspace}")

    # 1. Packages come from a local JSON file; nothing is fetched online.
    section("1. local package manifest (offline)")
    registry = PackageRegistry.from_file(Path(__file__).parent / "packages.json")
    release = registry.resolve("libwidget")
    rc = registry.resolve("libwidget", "1.0.0-rc.1")
    print(f"highest libwidget: {release.version} inputs={release.inputs}")
    print(f"pinned pre-release: {rc.version}")

    cache = BuildCache(workspace / "cache")
    builder = Builder(cache, work_root=workspace / "work")

    # 2. A node with two outputs publishes only when both pass digest checks.
    section("2. multi-output build (all outputs required)")
    node = BuildNode(
        "docs:1.0.0",
        ("index.html", "style.css"),
        lambda d: [
            (d / "index.html").write_bytes(b"<html>docs</html>"),
            (d / "style.css").write_bytes(b"body{}"),
        ],
    )
    print(builder.build(node))

    # 3. Rebuild is served from cache.
    section("3. repeat build")
    print(builder.build(node))

    # 4. Four threads repeat the same fresh key: one execution, shared result.
    section("4. concurrent duplicate builds")
    runs = {"n": 0}
    barrier = threading.Barrier(4)

    def shared_action(d):
        runs["n"] += 1
        (d / "data.bin").write_bytes(b"payload-v1")

    fresh = BuildNode("data:v1", ("data.bin",), shared_action)
    statuses = []

    def worker():
        barrier.wait(timeout=5)  # all threads enter build() together
        statuses.append(builder.build(fresh).status)

    threads = [threading.Thread(target=worker) for _ in range(4)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    print(f"action executions: {runs['n']}; statuses: {sorted(statuses)}")

    # 5. Missing output: nothing is published and staging is cleaned.
    section("5. missing output -> fail, no partial publish")
    bad = BuildNode(
        "partial:v1", ("a.txt", "b.txt"),
        lambda d: (d / "a.txt").write_bytes(b"only-a"),
    )
    try:
        builder.build(bad)
    except BuildError as exc:
        print(f"BuildError: {exc}")
    print(f"cache entry for 'partial:v1': {cache.lookup('partial:v1')}")
    print(f"staging leftovers: {list((workspace / 'work').iterdir())}")

    # 6. Action raising an exception also publishes nothing.
    section("6. failing action -> cleanup, no publish")
    def crash_action(d):
        raise RuntimeError("boom")

    crash = BuildNode("crash:v1", ("o",), crash_action)
    try:
        builder.build(crash)
    except RuntimeError as exc:
        print(f"RuntimeError: {exc}")
    print(f"cache entry for 'crash:v1': {cache.lookup('crash:v1')}")

    # 7. Forced rebuild with identical content is deduplicated.
    section("7. forced rebuild, identical content")
    same = BuildNode("data:v1", ("data.bin",),
                     lambda d: (d / "data.bin").write_bytes(b"payload-v1"))
    print(builder.build(same, force=True))

    # 8. Forced rebuild with different content is a conflict; first is kept.
    section("8. same key, different content -> non-deterministic conflict")
    changed = BuildNode("data:v1", ("data.bin",),
                        lambda d: (d / "data.bin").write_bytes(b"payload-v2"))
    try:
        builder.build(changed, force=True)
    except NonDeterministicConflict as exc:
        print(f"NonDeterministicConflict: {exc}")
    kept = cache.lookup("data:v1")
    print(f"kept bytes: {kept.read_output('data.bin')!r}")
    print(f"recorded conflicts: {len(cache.conflicts)}")

    shutil.rmtree(workspace, ignore_errors=True)
    print(f"\ncleaned up {workspace}")


if __name__ == "__main__":
    main()
