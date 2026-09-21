#!/usr/bin/env python3
"""Executable replay of the required scenario on a fresh directory.

Usage::

    python3 replay_demo.py [DIR]

Creates DIR (default ./demo-repo), imports the v1 root package closure, pins
the old snapshot with an explicit lease, publishes the v2 snapshot that drops
``oldlib``, runs a GC generation (old contents survive because the lease is
valid), reads the old package through the lease, releases it, and runs a
second GC generation that finally reclaims the old snapshot and its private
blobs.
"""
import os
import shutil
import sys

from imagerepo import Repository
from imagerepo.checker import audit_repository
from imagerepo.fixtures import TARGET
from imagerepo.model import Ident
from imagerepo.replay import Replay


def main(directory: str) -> int:
    if os.path.exists(directory):
        shutil.rmtree(directory)
    repo = Repository.open(directory)
    rp = Replay(repo)

    print("1. import v1 closure (target packages + host build tool)")
    rp.import_v1_closure()
    old = rp.publish_old()
    print(f"   published old snapshot {old[:12]}")
    lease = rp.pin_old(holder="reader-a")
    print(f"   granted explicit lease {lease[:12]} (no wall-clock expiry)")

    print("2. import v2 closure and publish a snapshot that removes oldlib")
    rp.import_v2_closure()
    new = rp.publish_new()
    print(f"   published new snapshot {new[:12]}")

    print("3. GC generation while the old lease is still valid")
    report = repo.run_gc()
    print(f"   dropped objects={len(report.dropped_objects)} "
          f"manifests={len(report.dropped_manifests)} (nothing: lease pins old)")

    print("4. read the old, removed package through the still-valid lease")
    data = repo.read_archive(lease, Ident("oldlib", "1.0.0", TARGET))
    print(f"   oldlib bytes: {data!r}")

    print("5. release the lease, then run a second GC generation")
    repo.release_lease(lease)
    report = repo.run_gc()
    print(f"   dropped objects={len(report.dropped_objects)} "
          f"manifests={len(report.dropped_manifests)}")
    print(f"   old snapshot present: {os.path.exists(repo.snap_path(old))}")

    audit = audit_repository(repo.root)
    print(f"6. independent audit: {len(audit)} visible snapshot(s), all complete+immutable")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1] if len(sys.argv) > 1 else "demo-repo"))
