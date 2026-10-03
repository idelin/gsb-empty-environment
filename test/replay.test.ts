import { test } from 'node:test';
import assert from 'node:assert/strict';
import { simulateMasterLeaseRace } from '../src/raceSimulation.ts';
import { AuditTrail } from '../src/auditTrail.ts';

test('replay orders events by logical time and renders a timeline', () => {
  const race = simulateMasterLeaseRace({ baseMs: 5_000, gapMs: 0.25 });
  const frames = race.trail.replay('master');
  assert.ok(frames.length >= 6);
  for (let i = 1; i < frames.length; i++) {
    assert.ok(frames[i]!.lamport > frames[i - 1]!.lamport, 'lamport increases along replay');
    assert.ok(frames[i]!.offsetMs >= frames[i - 1]!.offsetMs, 'offsets are non-decreasing');
  }
  assert.equal(frames[0]!.offsetMs, 0);
  const text = frames.map((f) => f.line).join('\n');
  assert.match(text, /REQUEST/);
  assert.match(text, /RACE!/);
  assert.match(text, /GRANT/);
  assert.match(text, /LOCK/);
  assert.match(text, /REJECT/);
});

test('causal chain of the rejection is a self-contained evidence path', () => {
  const race = simulateMasterLeaseRace();
  const chain = race.evidenceChain;
  // Every causal parent referenced inside the chain is itself in the chain.
  const ids = new Set(chain.map((e) => e.id));
  for (const e of chain) {
    for (const parent of e.causedBy) {
      assert.ok(ids.has(parent), `${parent} missing from evidence chain`);
    }
  }
  // Chain is ordered by logical time.
  for (let i = 1; i < chain.length; i++) {
    assert.ok(chain[i]!.lamport >= chain[i - 1]!.lamport);
  }
});

test('trail rejects events with unknown causal parents', () => {
  const trail = new AuditTrail();
  assert.throws(
    () =>
      trail.record({
        lamport: 1,
        wallClockMs: 0,
        type: 'LEASE_GRANTED',
        leaseId: 'L',
        causedBy: ['evt-999'],
        details: {},
      }),
    /causal parent evt-999/,
  );
});
