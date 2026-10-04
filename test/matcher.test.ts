import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PatternMatcher } from '../src/matcher.ts';
import { computeReferenceAlerts } from '../src/reference.ts';
import type { Change, EventRevision } from '../src/types.ts';

// ---------- helpers ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Materialized view over the change stream; seq-guarded so replays are no-ops. */
class AlertView {
  readonly alerts = new Map<string, { aId: string; bId: string }>();
  upserts = 0;
  retracts = 0;
  private lastSeq = 0;

  apply(changes: Iterable<Change>): void {
    for (const c of changes) {
      if (c.seq <= this.lastSeq) continue; // replay / duplicate delivery
      this.lastSeq = c.seq;
      if (c.kind === 'upsert') {
        this.alerts.set(c.alertId, { aId: c.aId, bId: c.bId });
        this.upserts += 1;
      } else {
        this.alerts.delete(c.alertId);
        this.retracts += 1;
      }
    }
  }
}

/** Naive set-semantics consumer without seq guard: naturally idempotent. */
class NaiveView {
  readonly alerts = new Map<string, { aId: string; bId: string }>();
  apply(changes: Iterable<Change>): void {
    for (const c of changes) {
      if (c.kind === 'upsert') this.alerts.set(c.alertId, { aId: c.aId, bId: c.bId });
      else this.alerts.delete(c.alertId);
    }
  }
}

function rev(
  id: string,
  revision: number,
  type: 'A' | 'B' | 'C',
  timestamp: number,
  retracted = false,
): EventRevision {
  return { id, revision, type, timestamp, retracted };
}

function alertKeys(view: { alerts: Map<string, unknown> }): string[] {
  return [...view.alerts.keys()].sort();
}

// ---------- 1. basic pattern semantics ----------

test('basic: A then B within inclusive window produces one alert', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 10)).changes); // exactly W away: inclusive
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
});

test('basic: B outside the window does not match', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 11)).changes);
  assert.deepEqual(alertKeys(view), []);
});

test('basic: B before A does not match', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('b1', 1, 'B', 0)).changes);
  view.apply(m.apply(rev('a1', 1, 'A', 5)).changes);
  assert.deepEqual(alertKeys(view), []);
});

test('basic: same-tick A and B match (window inclusive at both ends)', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('a1', 1, 'A', 5)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 5)).changes);
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
});

test('basic: C strictly between A and B invalidates; C outside does not', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 8)).changes);
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
  view.apply(m.apply(rev('c1', 1, 'C', 4)).changes); // inside [0,8]
  assert.deepEqual(alertKeys(view), []);
  // C before tA does not block
  const m2 = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view2 = new AlertView();
  view2.apply(m2.apply(rev('c9', 1, 'C', 1)).changes);
  view2.apply(m2.apply(rev('a1', 1, 'A', 5)).changes);
  view2.apply(m2.apply(rev('b1', 1, 'B', 9)).changes);
  assert.deepEqual(alertKeys(view2), ['alert:a1->b1']);
});

test('basic: C at the same tick as A or B also invalidates (inclusive ends)', () => {
  for (const cTs of [0, 8]) {
    const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
    const view = new AlertView();
    view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
    view.apply(m.apply(rev('b1', 1, 'B', 8)).changes);
    view.apply(m.apply(rev('c1', 1, 'C', cTs)).changes);
    assert.deepEqual(alertKeys(view), [], `C at ${cTs} should invalidate`);
  }
});

test('basic: all satisfying (A,B) pairs are emitted, not just nearest', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.applyAll([
    rev('a1', 1, 'A', 0),
    rev('a2', 1, 'A', 2),
    rev('b1', 1, 'B', 5),
    rev('b2', 1, 'B', 9),
  ]));
  assert.deepEqual(alertKeys(view), [
    'alert:a1->b1',
    'alert:a1->b2',
    'alert:a2->b1',
    'alert:a2->b2',
  ]);
});

// ---------- 2. revision handling ----------

test('revisions: higher revision may change time and re-trigger matching', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 20)).changes); // too far
  assert.deepEqual(alertKeys(view), []);
  view.apply(m.apply(rev('b1', 2, 'B', 7)).changes); // moved into window
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
});

test('revisions: higher revision may change type', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('x', 1, 'C', 4)).changes); // blocks
  view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 8)).changes);
  assert.deepEqual(alertKeys(view), []);
  view.apply(m.apply(rev('x', 2, 'B', 6)).changes); // C becomes a B
  assert.deepEqual(alertKeys(view), ['alert:a1->b1', 'alert:a1->x']);
});

test('revisions: retraction of A or B retracts the alert', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.applyAll([rev('a1', 1, 'A', 0), rev('b1', 1, 'B', 5)]));
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
  view.apply(m.apply(rev('b1', 2, 'B', 5, true)).changes);
  assert.deepEqual(alertKeys(view), []);
  // restore B, then retract A
  view.apply(m.apply(rev('b1', 3, 'B', 5)).changes);
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
  view.apply(m.apply(rev('a1', 2, 'A', 0, true)).changes);
  assert.deepEqual(alertKeys(view), []);
});

test('revisions: duplicate same-content revision is an idempotent no-op', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const first = m.apply(rev('a1', 1, 'A', 0));
  const dup = m.apply(rev('a1', 1, 'A', 0));
  assert.equal(first.status, 'applied');
  assert.equal(dup.status, 'duplicate');
  assert.deepEqual(dup.changes, []);
});

test('revisions: same revision with different content is a conflict, state untouched', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('a1', 1, 'A', 0)).changes);
  view.apply(m.apply(rev('b1', 1, 'B', 5)).changes);
  const conflict = m.apply(rev('b1', 1, 'B', 9)); // same rev, different time
  assert.equal(conflict.status, 'conflict');
  assert.deepEqual(conflict.changes, []);
  assert.deepEqual(m.conflicts, [{ id: 'b1', revision: 1 }]);
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']); // unchanged
});

test('revisions: lower revision is stale and ignored', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  m.apply(rev('a1', 5, 'A', 0));
  const res = m.apply(rev('a1', 3, 'B', 0));
  assert.equal(res.status, 'stale');
  assert.deepEqual(res.changes, []);
});

// ---------- 3. late C / recovery dynamics ----------

test('dynamics: late C retracts, C retraction recovers with upsert', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 100 });
  const view = new AlertView();
  view.apply(m.applyAll([rev('a1', 1, 'A', 0), rev('b1', 1, 'B', 8)]));
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);

  const lateC = m.apply(rev('c1', 1, 'C', 3));
  assert.deepEqual(lateC.changes.map((c) => c.kind), ['retract']);
  view.apply(lateC.changes);
  assert.deepEqual(alertKeys(view), []);

  const undoC = m.apply(rev('c1', 2, 'C', 3, true));
  assert.deepEqual(undoC.changes.map((c) => c.kind), ['upsert']);
  view.apply(undoC.changes);
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']);
});

test('dynamics: alert id is stable across retract/upsert cycles', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 100 });
  const log: Change[] = [];
  log.push(...m.applyAll([rev('a1', 1, 'A', 0), rev('b1', 1, 'B', 8)]));
  log.push(...m.apply(rev('c1', 1, 'C', 3)).changes);
  log.push(...m.apply(rev('c1', 2, 'C', 3, true)).changes);
  assert.deepEqual(
    log.map((c) => [c.kind, c.alertId]),
    [
      ['upsert', 'alert:a1->b1'],
      ['retract', 'alert:a1->b1'],
      ['upsert', 'alert:a1->b1'],
    ],
  );
  // seq is strictly increasing across the whole stream
  for (let i = 1; i < log.length; i += 1) assert.ok(log[i].seq > log[i - 1].seq);
});

// ---------- 4. watermark ----------

test('watermark: monotonic, lateness boundary is inclusive', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 5 });
  m.apply(rev('a1', 1, 'A', 100));
  m.advanceWatermark(50);
  m.advanceWatermark(30); // ignored: non-increasing
  assert.equal(m.currentWatermark, 50);
  // floor = 50 - 5 = 45; ts 45 accepted, ts 44 rejected
  assert.equal(m.apply(rev('b1', 1, 'B', 45)).status, 'applied');
  const late = m.apply(rev('c1', 1, 'C', 44));
  assert.equal(late.status, 'late');
  assert.deepEqual(late.changes, []);
});

test('watermark: rejected late revision does not change state', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 5 });
  const view = new AlertView();
  view.apply(m.applyAll([rev('a1', 1, 'A', 100), rev('b1', 1, 'B', 105)]));
  m.advanceWatermark(50);
  const before = m.snapshot();
  const late = m.apply(rev('c1', 1, 'C', 102)); // 102 < 50 - 5? no: 102 >= 45 -> applied
  assert.equal(late.status, 'applied');
  view.apply(late.changes);
  assert.deepEqual(alertKeys(view), []); // C at 102 blocks [100,105]
  // now a genuinely late retraction of that C must be rejected silently
  m.advanceWatermark(200); // floor = 195
  const res = m.apply(rev('c1', 2, 'C', 102, true));
  assert.equal(res.status, 'late');
  assert.deepEqual(res.changes, []);
  assert.deepEqual(alertKeys(view), []); // still blocked, state untouched
  void before;
});

test('watermark: old events are pruned, closed alerts archived, open windows kept', () => {
  const archived: string[] = [];
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 5 }, (p) =>
    archived.push(p.alertId),
  );
  const view = new AlertView();
  // old pair [0, 5] and a recent pair [100, 108]
  view.apply(m.applyAll([
    rev('a-old', 1, 'A', 0),
    rev('b-old', 1, 'B', 5),
    rev('a-new', 1, 'A', 100),
    rev('b-new', 1, 'B', 108),
  ]));
  assert.equal(m.liveEventCount, 4);
  // floor = 110 - 5 = 105, pruneBefore = 105 - 10 = 95
  const { prunedEvents, archivedPairs } = m.advanceWatermark(110);
  assert.equal(prunedEvents, 2); // a-old, b-old pruned
  assert.equal(archivedPairs, 1); // alert:a-old->b-old archived
  assert.deepEqual(archived, ['alert:a-old->b-old']);
  assert.equal(m.liveEventCount, 2); // recent events kept
  assert.deepEqual(alertKeys(view), ['alert:a-new->b-new', 'alert:a-old->b-old']);
  // a C beyond the lateness floor (105) is rejected...
  const late = m.apply(rev('c1', 1, 'C', 104));
  assert.equal(late.status, 'late');
  // ...but an allowed C inside the open window retracts the recent pair
  const c2 = m.apply(rev('c2', 1, 'C', 106));
  assert.equal(c2.status, 'applied');
  view.apply(c2.changes);
  assert.deepEqual(alertKeys(view), ['alert:a-old->b-old']);
});

// ---------- 5. batches vs single stream ----------

test('batches: chunked delivery equals single-shot delivery', () => {
  const rng = mulberry32(42);
  const events: EventRevision[] = [];
  for (let i = 0; i < 200; i += 1) {
    const types = ['A', 'B', 'C'] as const;
    events.push(rev(`e${i}`, 1, types[Math.floor(rng() * 3)], Math.floor(rng() * 300)));
  }
  const run = (batches: EventRevision[][]): Map<string, { aId: string; bId: string }> => {
    const m = new PatternMatcher({ windowMs: 25, allowedLateness: 0 });
    const view = new NaiveView();
    for (const b of batches) view.apply(m.applyAll(b));
    return view.alerts;
  };
  const single = run([events]);
  const chunked: EventRevision[][] = [];
  for (let i = 0; i < events.length; i += 7) chunked.push(events.slice(i, i + 7));
  const many = run(chunked);
  assert.deepEqual([...many.keys()].sort(), [...single.keys()].sort());
});

// ---------- 6. randomized out-of-order consistency with reference ----------

function generateRevisionLog(seed: number, count: number): EventRevision[] {
  const rng = mulberry32(seed);
  const revisions = new Map<string, number>();
  const out: EventRevision[] = [];
  const types = ['A', 'B', 'C'] as const;
  for (let i = 0; i < count; i += 1) {
    const id = `e${Math.floor(rng() * 40)}`;
    const next = (revisions.get(id) ?? 0) + 1;
    revisions.set(id, next);
    const type = types[Math.floor(rng() * 3)];
    const ts = Math.floor(rng() * 200);
    out.push(rev(id, next, type, ts, rng() < 0.08));
  }
  // full out-of-order delivery
  for (let i = out.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rng() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

test('randomized: streaming final alerts equal reference computation', () => {
  for (const seed of [1, 2, 3, 7, 99, 12345]) {
    const W = 20;
    const log = generateRevisionLog(seed, 600);
    const m = new PatternMatcher({ windowMs: W, allowedLateness: 0 });
    const view = new NaiveView();
    view.apply(m.applyAll(log));
    const expected = computeReferenceAlerts(log, W);
    assert.deepEqual(
      [...view.alerts.keys()].sort(),
      [...expected.keys()].sort(),
      `seed ${seed}: view vs reference`,
    );
    // operator's own open-pair state agrees too
    assert.deepEqual(
      [...m.openAlerts().keys()].sort(),
      [...expected.keys()].sort(),
      `seed ${seed}: operator state vs reference`,
    );
  }
});

test('randomized: same-tick heavy stream stays consistent with reference', () => {
  const rng = mulberry32(777);
  const revisions = new Map<string, number>();
  const log: EventRevision[] = [];
  const types = ['A', 'B', 'C'] as const;
  for (let i = 0; i < 400; i += 1) {
    const id = `e${Math.floor(rng() * 25)}`;
    const next = (revisions.get(id) ?? 0) + 1;
    revisions.set(id, next);
    // only 4 distinct ticks -> heavy same-timestamp collisions
    log.push(rev(id, next, types[Math.floor(rng() * 3)], Math.floor(rng() * 4), rng() < 0.05));
  }
  for (let i = log.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rng() * (i + 1));
    [log[i], log[j]] = [log[j], log[i]];
  }
  const W = 2;
  const m = new PatternMatcher({ windowMs: W, allowedLateness: 0 });
  const view = new NaiveView();
  view.apply(m.applyAll(log));
  const expected = computeReferenceAlerts(log, W);
  assert.deepEqual([...view.alerts.keys()].sort(), [...expected.keys()].sort());
});

// ---------- 7. same-tick conflicts ----------

test('conflicts: same id+revision with different content rejected, first wins', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
  const view = new AlertView();
  view.apply(m.apply(rev('x', 1, 'C', 5)).changes); // x is a C at tick 5
  const conflict = m.apply(rev('x', 1, 'A', 5)); // conflicting re-delivery
  assert.equal(conflict.status, 'conflict');
  view.apply(m.applyAll([rev('a1', 1, 'A', 0), rev('b1', 1, 'B', 8)]));
  // x stayed a C at 5, so it blocks [0,8]
  assert.deepEqual(alertKeys(view), []);
  assert.equal(m.conflicts.length, 1);
});

test('conflicts: deterministic outcome regardless of delivery duplicates', () => {
  const build = (extraDups: number): string[] => {
    const m = new PatternMatcher({ windowMs: 10, allowedLateness: 0 });
    const view = new NaiveView();
    const stream: EventRevision[] = [
      rev('a1', 1, 'A', 5),
      rev('b1', 1, 'B', 5), // same tick as a1
      rev('c1', 1, 'C', 5), // same tick C blocks
      rev('c1', 2, 'C', 5, true), // retracted again -> pair valid
    ];
    for (let i = 0; i < extraDups; i += 1) stream.push(rev('a1', 1, 'A', 5));
    view.apply(m.applyAll(stream));
    return [...view.alerts.keys()].sort();
  };
  assert.deepEqual(build(0), ['alert:a1->b1']);
  assert.deepEqual(build(5), ['alert:a1->b1']);
});

// ---------- 8. checkpoint / recovery ----------

test('checkpoint: restore mid-stream yields identical final alerts and seq', () => {
  const W = 20;
  const log = generateRevisionLog(2024, 500);
  const split = 300;

  const full = new PatternMatcher({ windowMs: W, allowedLateness: 0 });
  const fullView = new NaiveView();
  fullView.apply(full.applyAll(log));

  const pre = new PatternMatcher({ windowMs: W, allowedLateness: 0 });
  // downstream consumer checkpoints its own view alongside the operator
  const preChanges = pre.applyAll(log.slice(0, split));
  const snapshot = pre.snapshot();

  const restored = PatternMatcher.restore(snapshot);
  const restoredView = new NaiveView();
  restoredView.apply(preChanges); // consumer-side checkpoint
  restoredView.apply(restored.applyAll(log.slice(split)));

  assert.deepEqual(
    [...restoredView.alerts.keys()].sort(),
    [...fullView.alerts.keys()].sort(),
  );
  // seq continues monotonically after restore: merged log has no duplicates
  const seqs: number[] = [];
  const probe = PatternMatcher.restore(snapshot);
  for (const c of probe.applyAll(log.slice(split))) seqs.push(c.seq);
  for (let i = 1; i < seqs.length; i += 1) assert.ok(seqs[i] > seqs[i - 1]);
  // double restore from the same snapshot is deterministic
  const again = PatternMatcher.restore(snapshot);
  const againView = new NaiveView();
  againView.apply(preChanges);
  againView.apply(again.applyAll(log.slice(split)));
  assert.deepEqual(
    [...againView.alerts.keys()].sort(),
    [...restoredView.alerts.keys()].sort(),
  );
});

test('checkpoint: snapshot preserves watermark and lateness rejection', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 5 });
  m.apply(rev('a1', 1, 'A', 100));
  m.advanceWatermark(80);
  const restored = PatternMatcher.restore(m.snapshot());
  assert.equal(restored.currentWatermark, 80);
  assert.equal(restored.apply(rev('b1', 1, 'B', 70)).status, 'late'); // < 80-5
  assert.equal(restored.apply(rev('b1', 1, 'B', 105)).status, 'applied');
  assert.deepEqual([...restored.openAlerts().keys()], ['alert:a1->b1']);
});

// ---------- 9. change-stream replay ----------

test('replay: consuming the change stream twice does not double count', () => {
  const log = generateRevisionLog(555, 400);
  const m = new PatternMatcher({ windowMs: 15, allowedLateness: 0 });
  const changes = m.applyAll(log);

  const guarded = new AlertView();
  guarded.apply(changes);
  const upsertsAfterFirstPass = guarded.upserts;
  const retractsAfterFirstPass = guarded.retracts;
  const sizeAfterFirstPass = guarded.alerts.size;
  guarded.apply(changes); // full replay
  guarded.apply(changes); // and again
  assert.equal(guarded.upserts, upsertsAfterFirstPass);
  assert.equal(guarded.retracts, retractsAfterFirstPass);
  assert.equal(guarded.alerts.size, sizeAfterFirstPass);

  const naive = new NaiveView();
  naive.apply(changes);
  naive.apply(changes); // set semantics: idempotent
  assert.deepEqual(
    [...naive.alerts.keys()].sort(),
    [...guarded.alerts.keys()].sort(),
  );
});

// ---------- 10. randomized watermark + reference consistency ----------

test('randomized: watermark advancement, late rejection and archive stay consistent', () => {
  const rng = mulberry32(31337);
  const W = 10;
  const lateness = 15;
  const m = new PatternMatcher({ windowMs: W, allowedLateness: lateness });
  const view = new NaiveView();
  const accepted: EventRevision[] = [];
  const revisions = new Map<string, number>();
  const types = ['A', 'B', 'C'] as const;
  let maxTs = 0;
  // per-id revision chains delivered in per-id order, roughly time-ordered
  for (let i = 0; i < 800; i += 1) {
    const id = `e${Math.floor(rng() * 30)}`;
    const next = (revisions.get(id) ?? 0) + 1;
    revisions.set(id, next);
    maxTs += Math.floor(rng() * 3);
    const ts = Math.max(0, maxTs - Math.floor(rng() * 40)); // bounded disorder
    const r = rev(id, next, types[Math.floor(rng() * 3)], ts, rng() < 0.05);
    const res = m.apply(r);
    if (res.status === 'late') continue; // rejected: excluded from reference
    accepted.push(r);
    view.apply(res.changes);
    if (i % 25 === 0) m.advanceWatermark(maxTs - 5);
  }
  m.advanceWatermark(maxTs + 1000); // close everything
  const expected = computeReferenceAlerts(accepted, W);
  // view holds open + archived alerts (archived ones were never retracted)
  assert.deepEqual([...view.alerts.keys()].sort(), [...expected.keys()].sort());
  assert.ok(m.archivedAlertCount > 0, 'expected some alerts to be archived');
  // memory bound: after closing, live state is drained
  assert.equal(m.liveEventCount, 0);
  assert.equal(m.openAlertCount, 0);
});

test('watermark: revisions of pruned events are rejected (stale or late)', () => {
  const m = new PatternMatcher({ windowMs: 10, allowedLateness: 5 });
  const view = new AlertView();
  view.apply(m.applyAll([rev('a1', 1, 'A', 0), rev('b1', 1, 'B', 5)]));
  m.advanceWatermark(100); // pruneBefore = 85: a1/b1 pruned, pair archived
  assert.equal(m.liveEventCount, 0);
  assert.equal(m.archivedAlertCount, 1);
  // re-delivery of the pruned revision: stale no-op
  assert.equal(m.apply(rev('a1', 1, 'A', 0)).status, 'stale');
  // a newer revision of closed state: rejected as late, nothing changes
  const res = m.apply(rev('a1', 2, 'A', 500));
  assert.equal(res.status, 'late');
  assert.deepEqual(res.changes, []);
  assert.deepEqual(alertKeys(view), ['alert:a1->b1']); // archived alert stands
});
