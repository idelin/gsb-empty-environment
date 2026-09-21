"""End-to-end demo of the deterministic multi-output build cache.

Run from the repo root:  python3 demo.py
"""

import hashlib
import shutil
import threading
from pathlib import Path

from buildcache import (
    Builder,
    CacheStore,
    ConflictError,
    SummaryMismatchError,
    deterministic_build,
    load_packages,
    load_plan,
)
from buildcache.manifest import Node, OutputSpec, output_content

ROOT = Path(__file__).resolve().parent
DATA = ROOT / "data"
RUN = ROOT / "out" / "demo"


def fresh(name: str):
    path = RUN / name
    shutil.rmtree(path, ignore_errors=True)
    return CacheStore(path / "cache"), path / "work"


def staging_left(work: Path) -> bool:
    return work.exists() and any(work.iterdir())


def main() -> None:
    shutil.rmtree(RUN, ignore_errors=True)
    packages = load_packages(DATA / "packages.json")
    nodes = load_plan(DATA / "build_plan.json", packages)
    versions = ", ".join(f"{p.name} {p.version}" for p in packages.values())
    print(f"loaded {len(packages)} packages ({versions}), {len(nodes)} nodes from {DATA}")

    print("\n[1] cold build: publish only when every output passes the digest check")
    store, work = fresh("cold")
    builder = Builder(store, work)
    for node in nodes:
        entry = builder.build(node)
        outs = ", ".join(sorted(entry.outputs))
        print(f"    {node.id}: published {len(entry.outputs)} outputs ({outs})")

    print("\n[2] rebuild: identical key+content is deduplicated (cache hit)")
    calls = []

    def counting(node, staging):
        calls.append(node.id)
        deterministic_build(node, staging)

    builder = Builder(store, work, build_fn=counting)
    for node in nodes:
        builder.build(node)
    print(f"    {len(nodes)} nodes rebuilt -> {len(calls)} real build executions")

    print("\n[3] concurrent duplicate builds of the same key")
    store, work = fresh("concurrent")
    calls = []
    lock = threading.Lock()

    def counted(node, staging):
        with lock:
            calls.append(node.id)
        deterministic_build(node, staging)

    builder = Builder(store, work, build_fn=counted)
    results, errors = [], []

    def worker():
        try:
            results.append(builder.build(nodes[0]))
        except Exception as exc:  # noqa: BLE001 - demo collects all failures
            errors.append(exc)

    threads = [threading.Thread(target=worker) for _ in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    distinct = len({r.digest for r in results})
    print(f"    8 threads -> {len(calls)} real build, {distinct} distinct entry, {len(errors)} errors")

    print("\n[4] one output missing -> nothing is published")
    store, work = fresh("missing")

    def broken(node, staging):
        spec = node.outputs[0]
        target = Path(staging) / spec.path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(output_content(node.id, node.inputs, spec.path))

    builder = Builder(store, work, build_fn=broken)
    try:
        builder.build(nodes[0])
    except SummaryMismatchError as exc:
        print(f"    rejected: {exc}")
    print(f"    cache entry present: {store.get(nodes[0].key) is not None}; "
          f"staging left behind: {staging_left(work)}")

    print("\n[5] tampered output -> digest mismatch, partial outputs cleaned up")
    store, work = fresh("tampered")

    def tampered(node, staging):
        for spec in node.outputs:
            target = Path(staging) / spec.path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(b"tampered-by-build")

    builder = Builder(store, work, build_fn=tampered)
    try:
        builder.build(nodes[0])
    except SummaryMismatchError as exc:
        print(f"    rejected: {exc}")
    print(f"    cache entry present: {store.get(nodes[0].key) is not None}; "
          f"staging left behind: {staging_left(work)}")

    print("\n[6] forced rebuild with different artifacts -> non-determinism conflict, first write wins")
    store, work = fresh("conflict")
    builder = Builder(store, work)
    node = nodes[0]
    original = builder.build(node)

    def variant_content(path):
        return output_content(node.id, node.inputs, path) + b"#nondeterministic"

    specs = tuple(
        OutputSpec(s.path, hashlib.sha256(variant_content(s.path)).hexdigest())
        for s in node.outputs
    )
    node_v2 = Node(node.id, node.package, node.inputs, specs)

    def variant_build(n, staging):
        for spec in n.outputs:
            target = Path(staging) / spec.path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(variant_content(spec.path))

    try:
        Builder(store, work, build_fn=variant_build).build(node_v2, force=True)
    except ConflictError as exc:
        print(f"    conflict: {exc}")
    kept = store.get(node.key)
    print(f"    cached digest unchanged: {kept.digest == original.digest}")


if __name__ == "__main__":
    main()
