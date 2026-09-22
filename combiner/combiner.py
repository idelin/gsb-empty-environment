"""Window result combiner.

Merges sharded partial results into window results. Only mutually compatible
slices (same rule version, live shard, newest local version) are ever
combined, and each publish binds the exact version vector it was built from.

Distinct handling for the messy cases:

- missing shard   -> the window stays pending until its deadline
- old version     -> rejected as ``stale_version`` / ``duplicate``
- revocation      -> the slice is withdrawn; a published window is corrected
                     by a compensating event, never by editing history
- timeout         -> a partial publish flagged ``complete: false`` with the
                     list of missing shards
- scale in/out    -> a handover manifest retires the parent shard so it can
                     never be double counted together with its children

All state transitions are journaled to local files, so the combiner can be
replayed and recovered after a crash at any point.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .downstream import DownstreamSink
from .protocol import REVOKE, HandoverManifest, ShardReport, result_id_for
from .storage import Storage


class SimulatedCrash(Exception):
    """Crash injected between writing the publish record and the ack."""


@dataclass
class Slice:
    shard_id: str
    epoch: int
    local_version: int
    watermark: int
    rule_version: str
    value: float

    @staticmethod
    def from_report(report: ShardReport) -> "Slice":
        return Slice(report.shard_id, report.epoch, report.local_version,
                     report.watermark, report.rule_version, report.value)

    def to_dict(self) -> dict:
        return dict(self.__dict__)


@dataclass
class WindowState:
    window_id: str
    expected: set[str]
    deadline: int
    slices: dict[str, Slice] = field(default_factory=dict)
    retired: dict[str, int] = field(default_factory=dict)  # parent -> epoch
    status: str = "open"  # open | published | timed_out
    publish_seq: int = 0
    effective_value: float | None = None
    published_rule_version: str | None = None
    last_result_id: str | None = None

    def snapshot_dict(self) -> dict:
        return {
            "window_id": self.window_id,
            "expected": sorted(self.expected),
            "deadline": self.deadline,
            "slices": {shard: s.to_dict()
                       for shard, s in sorted(self.slices.items())},
            "retired": dict(sorted(self.retired.items())),
            "status": self.status,
            "publish_seq": self.publish_seq,
            "effective_value": self.effective_value,
            "published_rule_version": self.published_rule_version,
            "last_result_id": self.last_result_id,
        }


def replay_windows(records: list[dict]) -> dict[str, WindowState]:
    """Rebuild window states from journal records (used on recovery)."""
    windows: dict[str, WindowState] = {}
    for rec in records:
        kind = rec["type"]
        if kind == "window_opened":
            windows[rec["window_id"]] = WindowState(
                rec["window_id"], set(rec["expected"]), rec["deadline"])
        elif kind == "report_accepted":
            report = ShardReport.from_dict(rec["report"])
            window = windows[report.window_id]
            window.slices[report.shard_id] = Slice.from_report(report)
        elif kind == "slice_revoked":
            windows[rec["window_id"]].slices.pop(rec["shard_id"], None)
        elif kind == "manifest_applied":
            manifest = HandoverManifest.from_dict(rec["manifest"])
            window = windows[manifest.window_id]
            window.expected.discard(manifest.parent)
            window.expected.update(manifest.children)
            window.retired[manifest.parent] = manifest.epoch
            window.slices.pop(manifest.parent, None)
        elif kind == "window_timeout":
            windows[rec["window_id"]].status = "timed_out"
        elif kind == "published":
            record = rec["record"]
            window = windows[record["window_id"]]
            window.publish_seq = record["seq"]
            window.last_result_id = record["result_id"]
            window.published_rule_version = record["rule_version"]
            if record["kind"] == "publish":
                if window.status == "open":
                    window.status = "published"
                window.effective_value = record["value"]
            else:  # compensation
                window.effective_value = (
                    (window.effective_value or 0.0) + record["delta"])
    return windows


class WindowCombiner:
    def __init__(self, storage: Storage,
                 downstream: DownstreamSink | None = None):
        self.storage = storage
        self.downstream = downstream
        self.windows: dict[str, WindowState] = {}
        self.events: list[dict] = []
        # crash-injection hook: raise between downstream delivery and ack
        self.fail_before_ack = False

    # ------------------------------------------------------------------ setup
    def open_window(self, window_id: str, expected: set[str],
                    deadline: int) -> WindowState:
        window = WindowState(window_id, set(expected), deadline)
        self.windows[window_id] = window
        self._journal({"type": "window_opened", "window_id": window_id,
                       "expected": sorted(expected), "deadline": deadline})
        return window

    # ----------------------------------------------------------------- ingest
    def ingest(self, report: ShardReport) -> list[dict]:
        window = self.windows[report.window_id]
        if report.shard_id in window.retired:
            return [self._reject(window, report, "shard_retired")]
        if report.shard_id not in window.expected:
            return [self._reject(window, report, "unknown_shard")]
        if report.kind == REVOKE:
            return self._revoke(window, report)
        current = window.slices.get(report.shard_id)
        if current is not None:
            if current.local_version > report.local_version:
                return [self._reject(window, report, "stale_version")]
            if (current.local_version == report.local_version
                    and current.rule_version == report.rule_version):
                return [self._reject(window, report, "duplicate")]
            # same version but a different rule version: the shard re-emitted
            # its result under other rules; the newer report replaces it
        window.slices[report.shard_id] = Slice.from_report(report)
        events = [self._journal(
            {"type": "report_accepted", "report": report.to_dict()})]
        events += self._maybe_publish(window)
        return events

    def apply_manifest(self, manifest: HandoverManifest) -> list[dict]:
        window = self.windows[manifest.window_id]
        window.expected.discard(manifest.parent)
        window.expected.update(manifest.children)
        window.retired[manifest.parent] = manifest.epoch
        window.slices.pop(manifest.parent, None)
        events = [self._journal(
            {"type": "manifest_applied", "manifest": manifest.to_dict()})]
        events += self._maybe_publish(window)
        return events

    def tick(self, now: int) -> list[dict]:
        """Advance time; open windows past their deadline publish partial."""
        events: list[dict] = []
        for window in self.windows.values():
            if window.status != "open" or now <= window.deadline:
                continue
            window.status = "timed_out"
            events.append(self._journal(
                {"type": "window_timeout", "window_id": window.window_id,
                 "now": now}))
            group = self._compatible_group(window)
            if group is not None:
                rule_version, slices = group
                complete, missing = True, []
            else:
                rule_version = window.published_rule_version
                if rule_version is None and window.slices:
                    rule_version = sorted(
                        s.rule_version for s in window.slices.values())[0]
                slices = sorted(
                    (s for s in window.slices.values()
                     if s.rule_version == rule_version),
                    key=lambda s: s.shard_id)
                complete = False
                missing = sorted(
                    window.expected - {s.shard_id for s in slices})
            value = sum(s.value for s in slices)
            record = self._build_record(
                window, kind="publish", rule_version=rule_version,
                slices=slices, value=value, complete=complete, missing=missing)
            window.effective_value = value
            window.published_rule_version = rule_version
            events += self._commit(window, record)
        return events

    # --------------------------------------------------------------- recovery
    def recover(self) -> list[dict]:
        """Replay the journal and re-deliver published-but-unacked records.

        Re-delivery is safe because result ids are deterministic and the
        downstream dedupes by id: a crash between the publish-record write
        and the ack never leads to a double-applied delta.
        """
        self.windows = replay_windows(self.storage.read_journal())
        acked = set(self.storage.read_acks())
        reemitted: list[dict] = []
        for result_id, record in self.storage.read_published().items():
            if result_id not in acked:
                reemitted.append(record)
                self._deliver(record)
        return reemitted

    # --------------------------------------------------------------- internal
    def _revoke(self, window: WindowState,
                report: ShardReport) -> list[dict]:
        if report.shard_id not in window.slices:
            return [self._reject(window, report, "revoke_without_slice")]
        del window.slices[report.shard_id]
        events = [self._journal({
            "type": "slice_revoked", "window_id": window.window_id,
            "shard_id": report.shard_id,
            "local_version": report.local_version})]
        events += self._maybe_publish(window)
        return events

    def _reject(self, window: WindowState, report: ShardReport,
                reason: str) -> dict:
        return self._journal({
            "type": "report_rejected", "reason": reason,
            "window_id": window.window_id, "shard_id": report.shard_id,
            "epoch": report.epoch, "local_version": report.local_version,
            "rule_version": report.rule_version})

    @staticmethod
    def _compatible_group(window: WindowState):
        """The complete group of slices sharing one rule version, if any."""
        groups: dict[str, list[Slice]] = {}
        for slice_ in window.slices.values():
            groups.setdefault(slice_.rule_version, []).append(slice_)
        complete = {rv: group for rv, group in groups.items()
                    if window.expected <= {s.shard_id for s in group}}
        if not complete:
            return None
        if window.published_rule_version in complete:
            rule_version = window.published_rule_version
        else:
            rule_version = sorted(complete)[0]
        return rule_version, sorted(complete[rule_version],
                                    key=lambda s: s.shard_id)

    def _maybe_publish(self, window: WindowState) -> list[dict]:
        if window.status == "open":
            group = self._compatible_group(window)
            if group is None:
                return []  # still pending: missing or incompatible slices
            rule_version, slices = group
            value = sum(s.value for s in slices)
            record = self._build_record(
                window, kind="publish", rule_version=rule_version,
                slices=slices, value=value, complete=True, missing=[])
            window.status = "published"
            window.effective_value = value
            window.published_rule_version = rule_version
            return self._commit(window, record)
        # Already published: a changed slice triggers a compensating event
        # instead of silently rewriting the historical result.
        group = self._compatible_group(window)
        if group is not None:
            rule_version, slices = group
            new_value = sum(s.value for s in slices)
            complete, missing = True, []
        else:
            rule_version = window.published_rule_version
            slices = sorted(
                (s for s in window.slices.values()
                 if s.rule_version == rule_version),
                key=lambda s: s.shard_id)
            new_value = sum(s.value for s in slices)
            complete = False
            missing = sorted(window.expected - {s.shard_id for s in slices})
        if new_value == window.effective_value:
            return []
        record = self._build_record(
            window, kind="compensation", rule_version=rule_version,
            slices=slices, delta=new_value - window.effective_value,
            complete=complete, missing=missing,
            supersedes=window.last_result_id)
        window.effective_value = new_value
        return self._commit(window, record)

    def _build_record(self, window: WindowState, kind: str,
                      rule_version: str | None, slices: list[Slice],
                      value: float | None = None,
                      delta: float | None = None,
                      complete: bool = True,
                      missing: list[str] | None = None,
                      supersedes: str | None = None) -> dict:
        window.publish_seq += 1
        ordered = sorted(slices, key=lambda s: s.shard_id)
        record = {
            "window_id": window.window_id,
            "kind": kind,
            "seq": window.publish_seq,
            "rule_version": rule_version,
            "version_vector": {s.shard_id: s.local_version for s in ordered},
            "watermarks": {s.shard_id: s.watermark for s in ordered},
            "value": value,
            "delta": delta,
            "complete": complete,
            "missing": missing or [],
            "supersedes": supersedes,
        }
        record["result_id"] = result_id_for(record)
        return record

    def _commit(self, window: WindowState, record: dict) -> list[dict]:
        """Durable publish pipeline: intent -> record file -> journal -> ack.

        A crash anywhere in this sequence is recoverable: on restart the
        unacked record is re-delivered with the same deterministic id.
        """
        events = [self._journal({"type": "publish_intent",
                                 "result_id": record["result_id"]})]
        self.storage.write_published(record)
        window.last_result_id = record["result_id"]
        events.append(self._journal({"type": "published", "record": record}))
        self.storage.write_snapshot(window.window_id, window.publish_seq,
                                    window.snapshot_dict(),
                                    self.storage.journal_offset())
        self._deliver(record)
        return events

    def _deliver(self, record: dict) -> None:
        if self.downstream is not None:
            self.downstream.apply(record)
        if self.fail_before_ack:
            raise SimulatedCrash(
                f"crash after publish of {record['result_id']}, before ack")
        self.storage.append_ack(record["result_id"])
        self._journal({"type": "ack", "result_id": record["result_id"]})

    def _journal(self, record: dict) -> dict:
        self.storage.append_journal(record)
        self.events.append(record)
        return record
