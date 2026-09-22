# Revisable Alert State Machine

Python 3.11+, stdlib only, fully synthetic data.  Run:

```
python3 -m unittest discover -s tests -v
python3 main.py
```

## Design

The key split is **truth vs. notification**, managed in two layers:

**Truth layer** (`alerting/engine.py`) — pure functions of
(samples, rule version, suppression versions):

- Samples are keyed by *event time*; same-time corrections are resolved by
  ingestion sequence (`effective_samples`).
- `compute_states` builds the fault timeline as merged segments:
  `pending -> firing -> resolved` (plus `ok`), with hysteresis
  (enter/exit thresholds) and sustained durations measured in event time.
- `suppressed` is an overlay: a firing interval covered by a suppression
  window (`state_at`).
- `desired_notifications` maps episodes to what the world should see.
  A fault that recovers while suppressed produces **nothing** — the stale
  fault is never re-notified when suppression lifts.  A fault still firing
  when suppression lifts gets a deferred OPEN.

**Notification layer** (`alerting/system.py`, `alerting/outbox.py`):

- Rules and suppression windows are **versioned**; `rollback_rule` /
  `rollback_suppression` re-point the current version and `recompute()`
  re-derives history retroactively.
- `recompute()` bumps a revision and diffs desired notifications against
  the alert registry, emitting `OPEN` / `RESOLVED` / `REVOKED` events.
  Revocations and replacements reference the **original alert id** and the
  **revision** that caused them; replacements link `supersedes` /
  `superseded_by`.  The audit log is append-only — corrected alerts never
  disappear.
- The outbox is confirmable: `PENDING -> SENT -> ACKED`, idempotent by
  `alert_id|kind|revision`, JSON-persisted, so a crash between "recorded"
  and "confirmed" recovers without duplicate notifications or lost
  corrections.

## Tests (`tests/test_alerting.py`)

- Threshold 10 / duration 5: `11@0`, `12@6` fire at t=5; backfill `8@3`
  revokes the sent alert (original id + revision), audit keeps both events.
- Fault recovering inside a suppression window is never notified, even
  after the window lifts; still-firing faults get a deferred OPEN.
- Three overlapping rules (warn/crit/mid) evaluate independently.
- Rule rollback (v1 -> v2 -> v1) and suppression rollback produce
  revoke/re-open cycles with monotonic revisions.
- Same-timestamp sample correction (higher seq wins) triggers revocation.
- Outbox restart: crash between record/send/confirm, transport failure
  injection — no duplicates, no lost corrections.
- `IncrementalRule` (live path) is checked against full historical
  recompute on randomized streams; backfills flag `needs_recompute`.
