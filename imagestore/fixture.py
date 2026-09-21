"""Deterministic replay fixtures shared by the test-suite scenarios.

A :class:`Scenario` builds a small repository world:

* ``root`` 1.0 for target platform ``tgt-linux-x64`` has a runtime
  dependency on ``target-lib`` 2.1 and a *host build tool* dependency on
  ``host-gcc`` 9.4 for the host platform ``host-macos-arm64``.
* ``host-gcc`` 9.4 itself needs the host runtime library ``host-binutils``.
* The *old* snapshot pins the full graph.  The *new* snapshot removes
  ``target-lib`` (the root is rebuilt as 1.1 without that dependency).

Operations are expressed as a list of labelled steps.  The same step list is
executed fresh on an empty directory for every replay, which is what makes
fault injection and concurrency schedules reproducible.
"""

import hashlib
import os
import shutil
import threading

from . import events as events_mod
from .repo import ImageStore

TARGET = "tgt-linux-x64"
HOST = "host-macos-arm64"


def coord(name, version, platform=TARGET):
    return {"name": name, "version": version, "platform": platform}


def archive_bytes(name, version, salt=b""):
    """Deterministic synthetic archive payload."""
    return f"ARCHIVE {name} {version}\n".encode("utf-8") + salt


def digest_of(data):
    return hashlib.sha256(data).hexdigest()


def package(name, version, platform=TARGET, run=(), host=()):
    data = archive_bytes(name, version)
    meta = {
        "name": name, "version": version, "platform": platform,
        "run_requires": [coord(*_as_triple(dep)) for dep in run],
        "host_requires": [coord(*_as_triple(dep)) for dep in host],
    }
    return meta, data, digest_of(data), len(data)


def _as_triple(dep):
    if isinstance(dep, dict):
        return dep["name"], dep["version"], dep["platform"]
    if len(dep) == 2:
        name, version = dep
        return name, version, TARGET
    return tuple(dep)


def build_world_packages():
    """Return ``{label: (meta, data)}`` for the standard graph."""
    lib_meta, lib_data, _, _ = package("target-lib", "2.1", TARGET)
    bin_meta, bin_data, _, _ = package("host-binutils", "2.41", HOST)
    gcc_meta, gcc_data, _, _ = package(
        "host-gcc", "9.4", HOST, run=(("host-binutils", "2.41", HOST),)
    )
    root_meta, root_data, _, _ = package(
        "root", "1.0", TARGET,
        run=(("target-lib", "2.1", TARGET),),
        host=(("host-gcc", "9.4", HOST),),
    )
    root_new_meta, root_new_data, _, _ = package(
        "root", "1.1", TARGET,
        host=(("host-gcc", "9.4", HOST),),
    )
    return {
        "target-lib": (lib_meta, lib_data),
        "host-binutils": (bin_meta, bin_data),
        "host-gcc": (gcc_meta, gcc_data),
        "root-1.0": (root_meta, root_data),
        "root-1.1": (root_new_meta, root_new_data),
    }


def import_all(store):
    pkgs = build_world_packages()
    order = ["host-binutils", "host-gcc", "target-lib", "root-1.0", "root-1.1"]
    entries = {}
    for label in order:
        meta, data = pkgs[label]
        entries[label] = store.import_package_bytes(
            meta, data, declared_digest=digest_of(data),
            declared_length=len(data),
        )
    return entries


class Scenario:
    """Fresh-directory scenario runner with a reusable event bus."""

    def __init__(self, path):
        self.path = path
        if os.path.exists(path):
            shutil.rmtree(path)
        os.makedirs(path)
        self.bus = events_mod.EventBus()
        self.store = ImageStore.create(os.path.join(path, "repo"), bus=self.bus)
        self.records = []

    def reopen(self):
        """Simulate process restart: drop the instance and recover."""
        self.store = ImageStore.open(os.path.join(self.path, "repo"), bus=self.bus)
        return self.store

    def publish_old(self):
        import_all(self.store)
        sid = self.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        self.records.append(("publish-old", sid))
        return sid

    def lease_old(self):
        lease = self.store.acquire_lease("snap-old")
        self.records.append(("lease-old", lease.token))
        return lease

    def publish_new(self):
        sid = self.store.publish_snapshot(coord("root", "1.1"), "snap-new")
        self.records.append(("publish-new", sid))
        return sid

    def gc(self):
        return self.store.collect_garbage()


class ThreadedSchedule:
    """Run one operation in a background thread from an event hook."""

    def __init__(self, target, *, args=(), start_event=None, done_event=None):
        self.target = target
        self.args = args
        self.start_event = start_event or threading.Event()
        self.done_event = done_event or threading.Event()
        self.error = None
        self.result = None
        self.thread = threading.Thread(target=self._run, daemon=True)

    def _run(self):
        self.start_event.set()
        try:
            self.result = self.target(*self.args)
        except BaseException as exc:  # captured for the main thread assertions
            self.error = exc
        finally:
            self.done_event.set()

    def kick(self, _detail=None):
        self.thread.start()

    def wait(self, timeout=5):
        assert self.done_event.wait(timeout), "threaded operation hung"
        if isinstance(self.error, events_mod.InjectedCrash):
            raise self.error
        return self.result
