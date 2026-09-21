"""Persistent, revisable alerting engine.

Storage is sqlite (a file => outbox restarts are real process restarts).
Three layers are kept deliberately separate:

1. immutable inputs      (input_log, samples, threshold_versions,
                          suppression_versions)
2. derived canonical view (canonical_cache + alerts + alert_revisions) --
                          safe to delete and recompute at any time
3. notification outbox    (outbox, delivered) -- append-only, confirmable,
                          never rewritten; corrections can only be added
"""
from __future__ import annotations

import sqlite3
from .domain import (
    Episode, Sample, SuppressionVersion, ThresholdVersion,
    RESOLVED,
    evaluate,
)

SCHEMA = """
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v INTEGER);
CREATE TABLE IF NOT EXISTS input_log(
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,           -- sample | threshold | suppression
    payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS samples(
    t INTEGER NOT NULL, metric TEXT NOT NULL, value REAL NOT NULL,
    seq INTEGER NOT NULL,
    PRIMARY KEY(t, metric));
CREATE TABLE IF NOT EXISTS threshold_versions(
    rule_id TEXT, version INTEGER, valid_from INTEGER, metric TEXT,
    enter_at REAL, exit_at REAL, duration INTEGER, seq INTEGER,
    PRIMARY KEY(rule_id, version));
CREATE TABLE IF NOT EXISTS suppression_versions(
    rule_id TEXT, version INTEGER, valid_from INTEGER,
    window_start INTEGER, window_end INTEGER, metric TEXT, seq INTEGER,
    PRIMARY KEY(rule_id, version));

-- derived, deletable
CREATE TABLE IF NOT EXISTS canonical_cache(
    metric TEXT, rule_id TEXT, rule_version INTEGER, start_t INTEGER,
    end_t INTEGER, matures_at INTEGER, mature_t INTEGER, first_open_t INTEGER,
    coverage TEXT, PRIMARY KEY(metric, rule_id, rule_version, start_t));
CREATE TABLE IF NOT EXISTS alerts(
    id INTEGER PRIMARY KEY, metric TEXT, rule_id TEXT, rule_version INTEGER,
    start_t INTEGER, end_t INTEGER, mature_t INTEGER, first_open_t INTEGER,
    state TEXT, signature TEXT, ever_opened INTEGER DEFAULT 0,
    is_retracted INTEGER DEFAULT 0, revno INTEGER DEFAULT 0,
    last_notify_sig TEXT, last_notify_kind TEXT);
CREATE TABLE IF NOT EXISTS alert_revisions(
    id INTEGER NOT NULL, revno INTEGER NOT NULL,
    change TEXT NOT NULL,         -- created|replaced|retracted
    reason TEXT NOT NULL,
    state TEXT, signature TEXT NOT NULL,
    supersedes INTEGER,
    PRIMARY KEY(id, revno));

-- notifications
CREATE TABLE IF NOT EXISTS outbox(
    dedupe TEXT PRIMARY KEY,      -- id:revno:kind
    alert_id INTEGER NOT NULL,
    revno INTEGER NOT NULL,
    kind TEXT NOT NULL,           -- OPEN|RESOLVE|RETRACT|REPLACE
    state TEXT, created_seq INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS delivered(
    dedupe TEXT PRIMARY KEY, alert_id INTEGER, revno INTEGER,
    kind TEXT, state TEXT, t INTEGER);
CREATE TABLE IF NOT EXISTS pump_meta(k TEXT PRIMARY KEY, v TEXT);
"""


class Engine:
    def __init__(self, path: str = ":memory:"):
        self.conn = sqlite3.connect(path)
        self.conn.row_factory = sqlite3.Row
        self.conn.executescript(SCHEMA)
        self.conn.commit()
        self._replaying = False

    # ------------------------------------------------------------ inputs
    def _next_seq(self) -> int:
        cur = self.conn.execute("SELECT COALESCE(MAX(seq),0)+1 FROM input_log")
        return cur.fetchone()[0]

    def ingest_sample(self, t: int, metric: str, value: float) -> None:
        """Insert a sample; same (t, metric) overwrites (a correction)."""
        seq = self._next_seq()
        if not self._replaying:
            self.conn.execute(
                "INSERT INTO input_log(kind,payload) VALUES('sample',?)",
                (f"{t}|{metric}|{value}",))
        self._apply_sample(t, metric, value, seq=seq)

    def _apply_sample(self, t: int, metric: str, value: float,
                      seq: int | None = None) -> None:
        if seq is None:
            seq = self._next_seq()
        corrected = self.conn.execute(
            "SELECT 1 FROM samples WHERE t=? AND metric=?",
            (t, metric)).fetchone() is not None
        self.conn.execute(
            "INSERT OR REPLACE INTO samples(t,metric,value,seq) VALUES(?,?,?,?)",
            (t, metric, float(value), seq))
        self.conn.commit()
        self._recompute(min_event_t=t,
                        reason="sample-correction" if corrected else "sample")

    def add_threshold(self, rule_id: str, valid_from: int, metric: str,
                      enter_at: float, exit_at: float, duration: int,
                      version: int | None = None) -> int:
        seq = self._next_seq()
        if version is None:
            row = self.conn.execute(
                "SELECT COALESCE(MAX(version),0)+1 FROM threshold_versions "
                "WHERE rule_id=?", (rule_id,)).fetchone()
            version = row[0]
        if not self._replaying:
            self.conn.execute(
                "INSERT INTO input_log(kind,payload) VALUES('threshold',?)",
                (f"{rule_id}|{version}|{valid_from}|{metric}|{enter_at}|"
                 f"{exit_at}|{duration}",))
        self._apply_threshold(rule_id, version, valid_from, metric, enter_at,
                              exit_at, duration, seq=seq)
        return version

    def _apply_threshold(self, rule_id, version, valid_from, metric, enter_at,
                         exit_at, duration, seq=None):
        if seq is None:
            seq = self._next_seq()
        prior = self.conn.execute(
            "SELECT COUNT(*) FROM threshold_versions WHERE rule_id=?",
            (rule_id,)).fetchone()[0]
        reason = ("threshold-rollback" if prior >= 1
                  else "threshold-initial")
        self.conn.execute(
            "INSERT INTO threshold_versions VALUES(?,?,?,?,?,?,?,?)",
            (rule_id, version, valid_from, metric, enter_at, exit_at,
             duration, seq))
        self.conn.commit()
        self._recompute(min_event_t=valid_from, reason=reason)

    def rollback_threshold(self, rule_id: str, to_version: int,
                           valid_from: int) -> int:
        """Append a fresh version copying an older version's parameters."""
        row = self.conn.execute(
            "SELECT * FROM threshold_versions WHERE rule_id=? AND version=?",
            (rule_id, to_version)).fetchone()
        if row is None:
            raise KeyError(f"unknown version {rule_id} v{to_version}")
        return self.add_threshold(
            rule_id, valid_from, row["metric"], row["enter_at"],
            row["exit_at"], row["duration"])

    def add_suppression(self, rule_id: str, window_start: int,
                        window_end: int | None, metric: str | None = None,
                        valid_from: int | None = None,
                        version: int | None = None) -> int:
        seq = self._next_seq()
        if version is None:
            version = self.conn.execute(
                "SELECT COALESCE(MAX(version),0)+1 FROM suppression_versions "
                "WHERE rule_id=?", (rule_id,)).fetchone()[0]
        if valid_from is None:
            valid_from = window_start
        if not self._replaying:
            self.conn.execute(
                "INSERT INTO input_log(kind,payload) VALUES('suppression',?)",
                (f"{rule_id}|{version}|{valid_from}|{window_start}|"
                 f"{window_end}|{metric}",))
        self._apply_suppression(rule_id, version, valid_from, window_start,
                                window_end, metric, seq=seq)
        return version

    def _apply_suppression(self, rule_id, version, valid_from, window_start,
                           window_end, metric, seq=None):
        if seq is None:
            seq = self._next_seq()
        self.conn.execute(
            "INSERT INTO suppression_versions VALUES(?,?,?,?,?,?,?)",
            (rule_id, version, valid_from, window_start, window_end,
             metric, seq))
        self.conn.commit()
        self._recompute(min_event_t=valid_from, full=True,
                        reason="suppression-edit")

    def rollback_suppression(self, rule_id: str, to_version: int,
                             valid_from: int) -> int:
        row = self.conn.execute(
            "SELECT * FROM suppression_versions WHERE rule_id=? AND version=?",
            (rule_id, to_version)).fetchone()
        if row is None:
            raise KeyError(f"unknown version {rule_id} v{to_version}")
        return self.add_suppression(
            rule_id, row["window_start"], row["window_end"],
            row["metric"], valid_from=valid_from)

    # ------------------------------------------------------------- loading
    def _load_samples(self):
        return [Sample(r["t"], r["metric"], r["value"], r["seq"])
                for r in self.conn.execute(
                        "SELECT * FROM samples ORDER BY t, seq")]

    def _load_thresholds(self):
        return [ThresholdVersion(r["rule_id"], r["version"], r["valid_from"],
                                 r["metric"], r["enter_at"], r["exit_at"],
                                 r["duration"], r["seq"])
                for r in self.conn.execute(
                        "SELECT * FROM threshold_versions")]

    def _load_suppressions(self):
        return [SuppressionVersion(r["rule_id"], r["version"],
                                   r["valid_from"], r["window_start"],
                                   r["window_end"], r["metric"], r["seq"])
                for r in self.conn.execute(
                        "SELECT * FROM suppression_versions")]

    def _horizon(self):
        row = self.conn.execute("SELECT MAX(t) m FROM samples").fetchone()
        return row["m"] if row and row["m"] is not None else None

    # ----------------------------------------------------- recompute core
    def _recompute(self, min_event_t: int, full: bool = False,
                   reason: str = "recompute") -> None:
        """Incrementally (prefix-cached) recompute canonical episodes,
        rematerialize alerts (revision diff), and reconcile the outbox."""
        samples = self._load_samples()
        thvs = self._load_thresholds()
        sups = self._load_suppressions()
        horizon = self._horizon()
        if horizon is None:
            return

        all_metrics = {s.metric for s in samples}
        if full:
            cached = []
            seeds = {m: min(s.t for s in samples if s.metric == m)
                     for m in all_metrics}
        else:
            # Per metric, reuse only episodes fully settled before the
            # invalidation point. The boundary episode (end == min_event_t)
            # is dropped because the inserted fact owns that point and may
            # extend or erase it. Other metrics' prefixes are untouched.
            cached = self._load_cache()
            keep = []
            seeds = {}
            for ep in cached:
                if ep.end_t is not None and ep.end_t < min_event_t:
                    keep.append(ep)
                else:
                    seeds[ep.metric] = min(
                        seeds.get(ep.metric, min_event_t), ep.start_t)
            for m in all_metrics:
                seeds.setdefault(m, min_event_t)
            cached = keep

        episodes = list(cached)
        for metric, seed in seeds.items():
            tail_samples = [s for s in samples
                            if s.metric == metric and s.t >= seed]
            if tail_samples:
                episodes.extend(
                    evaluate(tail_samples, thvs, sups, horizon=horizon))

        self.conn.execute("DELETE FROM canonical_cache")
        for ep in episodes:
            self._save_cache(ep)
        self.conn.commit()
        self._rematerialize(episodes, horizon, reason=reason)

    def _load_cache(self):
        import json
        out = []
        for r in self.conn.execute("SELECT * FROM canonical_cache"):
            out.append(Episode(
                r["metric"], r["rule_id"], r["rule_version"], r["start_t"],
                r["end_t"], r["matures_at"], r["mature_t"],
                r["first_open_t"],
                json.loads(r["coverage"])))
        return out

    def _save_cache(self, ep: Episode):
        import json
        self.conn.execute(
            "INSERT OR REPLACE INTO canonical_cache "
            "VALUES(?,?,?,?,?,?,?,?,?)",
            (ep.metric, ep.rule_id, ep.rule_version, ep.start_t, ep.end_t,
             ep.matures_at, ep.mature_t, ep.first_open_t,
             json.dumps(ep.coverage)))

    # ----------------------------------------------- alert materialization
    def _rematerialize(self, episodes: list[Episode], horizon: int,
                       reason: str = "recompute") -> None:
        """Diff canonical episodes against stored alerts.

        Pairing (identity across retroactive change): same metric+rule and
        overlapping episode intervals, closest start wins. This makes
        threshold edits / corrections that move or split an incident resolve
        to retract+create / replace correctly.
        """
        old = [dict(r) for r in self.conn.execute(
            "SELECT * FROM alerts ORDER BY start_t, id")]
        used: set[int] = set()

        def overlaps(a, ep):
            if a["metric"] != ep.metric:
                return False
            ae = a["end_t"] if a["end_t"] is not None else 10**18
            ee = ep.end_t if ep.end_t is not None else 10**18
            # episodes occupy the half-open interval [start, end): episodes
            # that merely touch at a version boundary do NOT overlap
            interior = not (ae <= ep.start_t or ee <= a["start_t"])
            if not interior:
                return False
            # identical rule is required; a v1 episode and a v3 episode
            # touching at the boundary are different incidents.
            return a["rule_id"] == ep.rule_id

        pairs: list[tuple[dict | None, Episode | None]] = []
        remaining = list(episodes)
        for a in old:
            cands = [ep for ep in remaining if overlaps(a, ep)]
            if cands:
                ep = min(cands, key=lambda e: (
                    abs(e.start_t - a["start_t"]),
                    0 if (e.rule_version == a["rule_version"]
                          and e.start_t == a["start_t"]) else 1))
                remaining.remove(ep)
                used.add(a["id"])
                pairs.append((a, ep))
            else:
                pairs.append((a, None))
        for ep in remaining:
            pairs.append((None, ep))

        batch_seq = self.conn.execute(
            "SELECT COALESCE(MAX(seq),0) FROM input_log").fetchone()[0]
        for a, ep in pairs:
            if a is None:
                self._create_alert(ep, horizon, reason, batch_seq)
            elif ep is None:
                self._retract_alert(a, reason, batch_seq)
            else:
                self._replace_alert(a, ep, horizon, reason, batch_seq)
        self.conn.commit()

    def _sig(self, ep: Episode):
        import json
        return json.dumps(ep.signature(), separators=(",", ":"))

    def _next_alert_id(self):
        return self.conn.execute(
            "SELECT COALESCE(MAX(id),0)+1 FROM alerts").fetchone()[0]

    def _create_alert(self, ep: Episode, horizon: int, reason: str,
                      batch_seq: int) -> int:
        aid = self._next_alert_id()
        state = ep.state_at(horizon)
        sig = self._sig(ep)
        self.conn.execute(
            "INSERT INTO alerts(id,metric,rule_id,rule_version,start_t,end_t,"
            "mature_t,first_open_t,state,signature,ever_opened,is_retracted,"
            "revno) VALUES(?,?,?,?,?,?,?,?,?,?,0,0,1)",
            (aid, ep.metric, ep.rule_id, ep.rule_version, ep.start_t,
             ep.end_t, ep.mature_t, ep.first_open_t, state, sig))
        self._revision(aid, 1, "created", reason, state, sig, None)
        self._reconcile_notify(aid, ep, horizon, batch_seq,
                               prior_open=False, prior_resolve=False)
        return aid

    def _retract_alert(self, a: dict, reason: str, batch_seq: int) -> None:
        if a["is_retracted"]:
            return
        revno = a["revno"] + 1
        sig = "null"
        self.conn.execute(
            "UPDATE alerts SET is_retracted=1, state='retracted', signature=?,"
            "revno=? WHERE id=?", (sig, revno, a["id"]))
        self._revision(a["id"], revno, "retracted", reason, "retracted",
                       sig, a["revno"])
        # Correction is required only if a notification obligation existed.
        if a["ever_opened"]:
            self._enqueue(a["id"], revno, "RETRACT", "retracted",
                          sig, batch_seq)

    def _replace_alert(self, a: dict, ep: Episode, horizon: int,
                       reason: str, batch_seq: int) -> None:
        sig = self._sig(ep)
        state = ep.state_at(horizon)
        revived = bool(a["is_retracted"])
        changed = revived or sig != a["signature"]
        if not changed:
            return
        revno = a["revno"] + 1
        self.conn.execute(
            "UPDATE alerts SET metric=?,rule_version=?,start_t=?,end_t=?,"
            "mature_t=?,first_open_t=?,state=?,signature=?,is_retracted=0,"
            "revno=? WHERE id=?",
            (ep.metric, ep.rule_version, ep.start_t, ep.end_t, ep.mature_t,
             ep.first_open_t, state, sig, revno, a["id"]))
        self._revision(a["id"], revno, "replaced", reason, state, sig,
                       a["revno"])
        self._reconcile_notify(a["id"], ep, horizon, batch_seq,
                               prior_open=bool(a["ever_opened"]),
                               prior_resolve=self._has_kind(a["id"],
                                                            "RESOLVE"),
                               revived=revived, revno=revno)

    def _revision(self, aid: int, revno: int, change: str, reason: str,
                  state: str | None, sig: str, supersedes: int | None):
        self.conn.execute(
            "INSERT INTO alert_revisions VALUES(?,?,?,?,?,?,?)",
            (aid, revno, change, reason, state, sig, supersedes))

    def _has_kind(self, aid: int, kind: str) -> bool:
        return self.conn.execute(
            "SELECT 1 FROM outbox WHERE alert_id=? AND kind=? LIMIT 1",
            (aid, kind)).fetchone() is not None

    # ------------------------------------------------- notification outbox
    def _enqueue(self, aid: int, revno: int, kind: str, state: str | None,
                 sig: str, batch_seq: int) -> None:
        dedupe = f"{aid}:{revno}:{kind}"
        self.conn.execute(
            "INSERT OR IGNORE INTO outbox(dedupe,alert_id,revno,kind,state,"
            "created_seq) VALUES(?,?,?,?,?,?)",
            (dedupe, aid, revno, kind, state, batch_seq))
        self.conn.execute(
            "UPDATE alerts SET last_notify_sig=?, last_notify_kind=? WHERE id=?",
            (sig, kind, aid))

    def _reconcile_notify(self, aid: int, ep: Episode, horizon: int,
                          batch_seq: int, prior_open: bool,
                          prior_resolve: bool, revived: bool = False,
                          revno: int | None = None) -> None:
        """Derive the *notification obligation* from the real-fault episode.

        Notifying is a separate axis from the fault state:
        * OPEN only for an incident that was mature and unsuppressed while
          still alive at a known time -- never for one already resolved when
          we first learn of it (suppression window retroactivity, late data);
        * RESOLVE only when an OPEN(ed) incident ends;
        * RETRACT only for something previously opened that vanished;
        * REPLACE keeps already-notified listeners in sync on live changes.
        """
        sig = self._sig(ep)
        if revno is None:
            revno = self.conn.execute(
                "SELECT revno FROM alerts WHERE id=?", (aid,)).fetchone()[0]
        row = self.conn.execute(
            "SELECT last_notify_sig,last_notify_kind FROM alerts WHERE id=?",
            (aid,)).fetchone()
        last_sig, last_kind = row[0], row[1]
        resolved = ep.end_t is not None
        openable = (not resolved and ep.mature_t is not None
                    and ep.first_open_t is not None
                    and ep.first_open_t <= horizon)

        # 1) prior OPEN contradicted by recomputation: the incident never
        # really matured (correction moved/shortened it) => it is false.
        if prior_open and ep.mature_t is None and not revived \
                and last_kind != "RETRACT":
            self._enqueue(aid, revno, "RETRACT", "retracted", sig, batch_seq)
            self._set_nonopen(aid)
            return

        # 2) (re)birth of a notifiable open incident
        if (revived or not prior_open) and openable:
            self._enqueue(aid, revno, "OPEN", ep.state_at(horizon), sig,
                          batch_seq)
            self._set_open(aid)
            return

        # 3) natural lifecycle end for something we told the world was open
        if prior_open and resolved and last_kind in ("OPEN", "REPLACE"):
            self._enqueue(aid, revno, "RESOLVE", RESOLVED, sig, batch_seq)
            return

        # 4) still outstanding (firing or suppressed), live shape changed:
        #    suppression cover and retroactive threshold edits land here
        if prior_open and not resolved and sig != last_sig \
                and last_kind in ("OPEN", "REPLACE"):
            self._enqueue(aid, revno, "REPLACE", ep.state_at(horizon), sig,
                          batch_seq)
        # pending/never-open/already-resolved-notified: no obligation.

    def _set_open(self, aid: int) -> None:
        self.conn.execute(
            "UPDATE alerts SET ever_opened=1 WHERE id=?", (aid,))

    def _set_nonopen(self, aid: int) -> None:
        self.conn.execute(
            "UPDATE alerts SET ever_opened=0 WHERE id=?", (aid,))

    # ------------------------------------------------- replay / snapshots
    @classmethod
    def replay(cls, path: str = ":memory:") -> "Engine":
        """Rebuild a fresh engine from an immutable input_log copy.

        Every table except input_log is wiped and reconstructed (full-history
        recompute): the resulting alerts/outbox must equal the incremental
        engine's. Suppression edits arrive in recorded arrival order and
        corrections overwrite same-t rows."""
        src = sqlite3.connect(path)
        src.row_factory = sqlite3.Row
        rows = src.execute(
            "SELECT kind,payload FROM input_log ORDER BY seq").fetchall()
        src.close()
        eng = cls(path)
        for tbl in ("samples", "threshold_versions", "suppression_versions",
                    "canonical_cache", "alerts", "alert_revisions",
                    "outbox", "delivered", "pump_meta", "meta", "input_log"):
            eng.conn.execute(f"DELETE FROM {tbl}")
        rows = list(rows)
        eng._replaying = True
        seqno = 0
        try:
            for r in rows:
                seqno += 1
                p = r["payload"].split("|")
                if r["kind"] == "sample":
                    eng._apply_sample(int(p[0]), p[1], float(p[2]), seq=seqno)
                elif r["kind"] == "threshold":
                    eng._apply_threshold(p[0], int(p[1]), int(p[2]), p[3],
                                         float(p[4]), float(p[5]), int(p[6]),
                                         seq=seqno)
                else:
                    we = None if p[4] == "None" else int(p[4])
                    met = None if p[5] == "None" else p[5]
                    eng._apply_suppression(p[0], int(p[1]), int(p[2]),
                                           int(p[3]), we, met, seq=seqno)
        finally:
            eng._replaying = False
        return eng

    def alert_snapshot(self):
        """Notification-independent view of truth, sorted for comparison."""
        rows = self.conn.execute(
            "SELECT metric,rule_id,rule_version,start_t,end_t,mature_t,"
            "first_open_t,state,is_retracted,ever_opened,revno "
            "FROM alerts ORDER BY id").fetchall()
        return [tuple(r) for r in rows]

    def outbox_snapshot(self):
        rows = self.conn.execute(
            "SELECT alert_id,revno,kind,state FROM outbox ORDER BY dedupe")
        return sorted((r["alert_id"], r["revno"], r["kind"], r["state"])
                      for r in rows)

    def revision_snapshot(self):
        rows = self.conn.execute(
            "SELECT id,revno,change,reason,state,supersedes "
            "FROM alert_revisions ORDER BY id,revno")
        return [tuple(r) for r in rows]

    def pending_count(self):
        return self.conn.execute(
            "SELECT COUNT(*) FROM outbox o WHERE NOT EXISTS("
            "SELECT 1 FROM delivered d WHERE d.dedupe=o.dedupe)").fetchone()[0]

    def delivered_snapshot(self):
        rows = self.conn.execute(
            "SELECT alert_id,kind FROM delivered ORDER BY t,dedupe")
        return [(r["alert_id"], r["kind"]) for r in rows]

    def notifications_snapshot(self):
        """All notification events (pending + confirmed) by dedupe key.
        Corrections only get added here; they never disappear."""
        out = {}
        for r in self.conn.execute("SELECT * FROM outbox"):
            out[r["dedupe"]] = (r["alert_id"], r["revno"], r["kind"],
                                r["state"], "pending")
        for r in self.conn.execute("SELECT * FROM delivered"):
            out.setdefault(r["dedupe"],
                           (r["alert_id"], r["revno"], r["kind"], r["state"],
                            "confirmed"))
        return sorted(out.items())

    def notification_kinds(self):
        return [v[2] for _, v in self.notifications_snapshot()]

    def notification_contents(self):
        """Replay-stable content view: dedupe -> (id, rev, kind, state).
        Delivery confirmation is a transport side effect, not part of the
        derived truth, so it is intentionally excluded."""
        return sorted((k, v[:4])
                      for k, v in self.notifications_snapshot())
