"""Scripted replay of the required end-to-end scenario.

The steps are split at the same linearization points the repository exposes,
which lets tests inject crashes or interleave other operations at each
boundary (see the concurrency and crash tests).
"""
from __future__ import annotations

from .fixtures import TARGET, load_json
from .model import HOST_PLATFORM, Ident


class Replay:
    def __init__(self, repo, fixture=None):
        self.repo = repo
        self.world = load_json(fixture) if fixture else load_json(
            __import__("imagerepo.fixtures", fromlist=["fixture_path"]).fixture_path())
        self.snap_old = None
        self.snap_new = None
        self.lease_old = None

    def import_one(self, ident):
        spec = self.world[ident]
        return self.repo.import_package(
            ident, spec["payload"],
            runtime_deps=spec["runtime_deps"], build_deps=spec["build_deps"])

    def import_v1_closure(self):
        """Import the root package plus its target and host-tool closure."""
        order = [
            Ident("gcc", "1.0.0", HOST_PLATFORM),
            Ident("libc", "1.0.0", TARGET),
            Ident("oldlib", "1.0.0", TARGET),
            Ident("app", "1.0.0", TARGET),
        ]
        for ident in order:
            self.import_one(ident)

    def publish_old(self):
        self.snap_old = self.repo.publish([Ident("app", "1.0.0", TARGET)])
        return self.snap_old

    def pin_old(self, holder="reader-a"):
        self.lease_old = self.repo.grant_lease(self.snap_old, holder=holder)
        return self.lease_old

    def import_v2_closure(self):
        for ident in [
            Ident("gcc", "1.1.0", HOST_PLATFORM),
            Ident("newlib", "1.0.0", TARGET),
            Ident("app", "2.0.0", TARGET),
        ]:
            self.import_one(ident)

    def publish_new(self):
        self.snap_new = self.repo.publish([Ident("app", "2.0.0", TARGET)])
        return self.snap_new

    def read_old_via_lease(self):
        return self.repo.read_archive(self.lease_old, Ident("oldlib", "1.0.0", TARGET))

    def release_old(self):
        self.repo.release_lease(self.lease_old)
        self.lease_old = None
