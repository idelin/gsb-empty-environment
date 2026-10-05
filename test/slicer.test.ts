import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import type { EventLog, LogEvent, VarVersion } from '../src/model.ts';
import { sliceLog } from '../src/slicer.ts';
import { replaySlice } from '../src/replay.ts';
import { verifySlice } from '../src/verify.ts';

const ref = (name: string, version: number): VarVersion => ({ name, version });
const lit = (value: number | boolean) => ({ op: 'lit' as const, value });

function write(id: number, name: string, version: number, value: number, reads: VarVersion[] = [], control: number[] = []): LogEvent {
  return { kind: 'write', id, target: ref(name, version), expr: lit(value), value, reads, control };
}

function loadLog(path: string): EventLog {
  return JSON.parse(readFileSync(path, 'utf8')) as EventLog;
}

test('backward slice keeps causal chain and drops irrelevant events', () => {
  const log = loadLog(new URL('../examples/complete-log.json', import.meta.url).pathname);
  const slice = sliceLog(log, 8);
  const ids = slice.events.map((e) => e.id);
  assert.deepEqual(ids, [1, 2, 4, 5, 6, 7, 8]);
  assert.ok(!ids.includes(3), 'irrelevant write msg@1 must be excluded');
  assert.deepEqual(slice.gaps, []);
});

test('slice is keyed by (name, version), not merged by variable name', () => {
  const log: EventLog = {
    events: [
      write(1, 'x', 1, 100),
      write(2, 'x', 3, 300),
      {
        kind: 'assert', id: 3, label: 'uses x@2',
        expr: { op: 'eq', lhs: { op: 'ref', ref: ref('x', 2) }, rhs: lit(200) },
        expected: true, value: false, passed: false,
        reads: [ref('x', 2)], control: [],
      },
    ],
  };
  const slice = sliceLog(log, 3);
  assert.deepEqual(slice.events.map((e) => e.id), [3]);
  assert.equal(slice.gaps.length, 1);
  const gap = slice.gaps[0];
  assert.equal(gap.type, 'missing-producer');
  if (gap.type === 'missing-producer') {
    assert.deepEqual(gap.ref, ref('x', 2));
    assert.equal(gap.neededBy, 3);
  }
});

test('control dependencies pull in the controlling cond and its own deps', () => {
  const log = loadLog(new URL('../examples/complete-log.json', import.meta.url).pathname);
  const slice = sliceLog(log, 8);
  const cond = slice.events.find((e) => e.id === 5);
  assert.ok(cond && cond.kind === 'cond', 'cond event #5 must be in slice via control dep of #6');
});

test('missing producer is reported as an explicit gap', () => {
  const log = loadLog(new URL('../examples/gapped-log.json', import.meta.url).pathname);
  const slice = sliceLog(log, 8);
  assert.deepEqual(slice.gaps, [
    { type: 'missing-producer', ref: { name: 'a', version: 2 }, neededBy: 7 },
  ]);
});

test('replay on a complete slice reproduces the recorded assertion value', () => {
  const log = loadLog(new URL('../examples/complete-log.json', import.meta.url).pathname);
  const report = verifySlice(log, 8);
  assert.equal(report.ok, true);
  assert.equal(report.unverifiable, false);
  assert.equal(report.replay.assertion?.recorded, false);
  assert.equal(report.replay.assertion?.replayed, false);
  assert.equal(report.replay.assertion?.match, true);
});

test('replay with a gap is unverifiable and reports the missing version', () => {
  const log = loadLog(new URL('../examples/gapped-log.json', import.meta.url).pathname);
  const report = verifySlice(log, 8);
  assert.equal(report.ok, false);
  assert.equal(report.unverifiable, true);
  assert.ok(report.replay.gaps.some((g) => g.eventId === 7 && g.missing.name === 'a' && g.missing.version === 2));
});

test('replay detects tampered recorded values as mismatches', () => {
  const log = loadLog(new URL('../examples/complete-log.json', import.meta.url).pathname);
  const tampered: EventLog = {
    events: log.events.map((e) => (e.id === 7 ? { ...e, value: 99 } : e)),
  };
  const slice = sliceLog(tampered, 8);
  const replay = replaySlice(slice.events, 8);
  assert.ok(replay.mismatches.some((m) => m.eventId === 7 && m.recorded === 99 && m.replayed === 7));
});

test('missing control event is reported as an explicit gap', () => {
  const log: EventLog = {
    events: [
      write(1, 'y', 1, 5, [], [42]),
      {
        kind: 'assert', id: 2, label: 'y',
        expr: { op: 'eq', lhs: { op: 'ref', ref: ref('y', 1) }, rhs: lit(5) },
        expected: true, value: true, passed: false,
        reads: [ref('y', 1)], control: [],
      },
    ],
  };
  const slice = sliceLog(log, 2);
  assert.deepEqual(slice.gaps, [{ type: 'missing-control-event', eventId: 42, neededBy: 1 }]);
});
