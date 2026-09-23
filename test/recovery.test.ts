import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CheckpointSpliceError,
  computeHash,
  GENESIS,
  interpretFullLog,
  LogBuilder,
  LogCorruptionError,
  MissingOperationError,
  RecoveryCancelledError,
  RecoveryEngine,
  type LogRecord,
} from '../src/index.ts';
import { StateStore, type JsonValue } from '../src/state.ts';

/** Standard log: branches, committed + aborted txs, side effect + ack, checkpoint. */
function buildLog(effectCalls: string[] = []): LogBuilder {
  const b = new LogBuilder();
  b.set('user:1', { name: 'ada' }); //                                   seq 0
  b.set('user:2', { name: 'grace' }); //                                 seq 1
  b.begin('tx-transfer'); //                                            seq 2
  b.set('acct:a', 100); //                                              seq 3 (buffered)
  b.set('acct:b', -100); //                                             seq 4 (buffered)
  b.commit(); //                                                        seq 5
  b.createBranch('feature'); //                                         seq 6
  b.switchBranch('feature'); //                                         seq 7
  b.set('flag:x', true); //                                             seq 8
  b.effect('email-1', { to: 'ada@example.com' }, (id) => { //            seq 9 + ack seq 10
    effectCalls.push(id);
    return { sent: true };
  });
  b.checkpoint('cp1'); //                                               seq 11
  b.switchBranch('main'); //                                            seq 12
  b.begin('tx-temp'); //                                                seq 13
  b.set('tmp', 1); //                                                   seq 14 (buffered)
  b.abort(); //                                                         seq 15
  b.del('user:2'); //                                                   seq 16
  return b;
}

function recover(serialized: string, handler?: (id: string, a: JsonValue) => JsonValue) {
  const engine = new RecoveryEngine({ handler });
  engine.load(serialized);
  engine.runToEnd();
  return engine;
}

test('full replay: recovery digest equals original state and independent interpreter', () => {
  const b = buildLog();
  const engine = recover(b.serialize());
  const truth = interpretFullLog(b.getRecords());
  assert.equal(engine.digest(), b.state.digest());
  assert.equal(engine.digest(), truth.digest);
  assert.deepEqual(engine.getState(), b.state.snapshot());
});

test('every prefix of the recovery matches the independent interpreter (per-failure-point cross-check)', () => {
  const b = buildLog();
  const records = b.getRecords();
  const engine = new RecoveryEngine();
  engine.load(b.serialize());
  const empty = new StateStore();
  assert.equal(engine.digest(), empty.digest());
  for (let i = 0; i < records.length; i++) {
    engine.step();
    const truth = interpretFullLog(records.slice(0, i + 1));
    assert.equal(engine.digest(), truth.digest, `digest mismatch after applying seq ${i}`);
  }
});

test('readers never see transaction intermediate state', () => {
  const b = buildLog();
  const engine = new RecoveryEngine();
  engine.load(b.serialize());
  const seen: Array<Record<string, JsonValue>> = [];
  for (let i = 0; i < 6; i++) {
    engine.step();
    seen.push(engine.getState());
  }
  assert.equal(seen[3]['acct:a'], undefined, 'buffered tx op leaked after seq 3');
  assert.equal(seen[4]['acct:b'], undefined, 'buffered tx op leaked after seq 4');
  assert.equal(seen[5]['acct:a'], 100, 'committed tx op missing after commit');
  assert.equal(seen[5]['acct:b'], -100, 'committed tx op missing after commit');
  assert.ok(Object.isFrozen(seen[5]), 'reader snapshot must be frozen');
});

test('aborted and uncommitted transactions are discarded', () => {
  const b = buildLog();
  const engine = recover(b.serialize());
  assert.equal(engine.getState()['tmp'], undefined, 'aborted tx op must not be visible');

  const dangling = new LogBuilder();
  dangling.set('a', 1);
  dangling.begin('tx-never-committed');
  dangling.set('b', 2);
  const engine2 = recover(dangling.serialize());
  assert.deepEqual(engine2.getState(), { a: 1 });
});

test('duplicate operation IDs are not replayed twice', () => {
  const b = new LogBuilder();
  b.set('k', 'original'); // opId op-0
  b.set('k', 'tampered', 'op-0'); // duplicate opId: valid chain, must be skipped
  const engine = recover(b.serialize());
  assert.equal(engine.getState()['k'], 'original');
  assert.equal(engine.digest(), interpretFullLog(b.getRecords()).digest);
});

test('missing intermediate operations cannot be skipped', () => {
  const b = buildLog();
  const lines = b.serialize().trimEnd().split('\n');
  for (const gap of [0, 5, 9]) {
    const spliced = lines.filter((_, i) => i !== gap).join('\n') + '\n';
    assert.throws(() => new RecoveryEngine().load(spliced), (e: unknown) => {
      assert.ok(e instanceof MissingOperationError, `expected MissingOperationError for gap ${gap}`);
      assert.equal((e as MissingOperationError).seq, gap);
      return true;
    });
  }
  // A clean tail truncation is a valid prefix and must still load.
  const prefix = lines.slice(0, 10).join('\n') + '\n';
  assert.equal(new RecoveryEngine().load(prefix), 10);
});

test('corruption at any record is located to its exact seq (all failure points)', () => {
  const b = buildLog();
  const lines = b.serialize().trimEnd().split('\n');
  for (let i = 0; i < lines.length; i++) {
    const rec = JSON.parse(lines[i]) as LogRecord;
    rec.payload = { ...rec.payload, corrupted: true }; // tamper without rehashing
    const corrupted = lines.map((l, j) => (j === i ? JSON.stringify(rec) : l)).join('\n') + '\n';
    assert.throws(() => new RecoveryEngine().load(corrupted), (e: unknown) => {
      assert.ok(e instanceof LogCorruptionError, `seq ${i}: expected LogCorruptionError, got ${e}`);
      assert.equal((e as LogCorruptionError).seq, i, `corruption at seq ${i} reported wrong seq`);
      return true;
    });
  }
});

test('a corrupt log yields no partial success state', () => {
  const b = buildLog();
  const lines = b.serialize().trimEnd().split('\n');
  const rec = JSON.parse(lines[8]) as LogRecord;
  rec.payload = { corrupted: true };
  lines[8] = JSON.stringify(rec);
  const engine = new RecoveryEngine();
  assert.throws(() => engine.load(lines.join('\n') + '\n'), LogCorruptionError);
  assert.equal(engine.recordCount, 0);
  assert.equal(engine.step().done, true, 'no record may be applied from a rejected log');
});

test('checkpoint splice with a foreign prefix digest is rejected', () => {
  const a = buildLog();
  const engineA = recover(a.serialize());
  const foreignCp = engineA.checkpoint('foreign');

  const other = new LogBuilder();
  other.set('completely', 'different');
  other.set('log', 'here');
  const engineB = new RecoveryEngine();
  engineB.load(other.serialize());
  assert.throws(() => engineB.restore(foreignCp), CheckpointSpliceError);

  // Tampered snapshot is rejected even on the matching log.
  const engineC = recover(a.serialize());
  const tampered = { ...foreignCp, snapshot: { ...foreignCp.snapshot, injected: 1 } };
  assert.throws(() => engineC.restore(tampered), CheckpointSpliceError);

  // Checkpoint from beyond the log end is rejected.
  const beyond = { ...foreignCp, seq: 999 };
  assert.throws(() => engineC.restore(beyond), CheckpointSpliceError);
});

test('checkpoint record bound to a wrong state digest is rejected during replay', () => {
  const mk = (seq: number, type: string, payload: Record<string, unknown>, prevHash: string) => {
    const r = { seq, branch: 'main', opId: `m-${seq}`, type, payload, prevHash, hash: '' };
    r.hash = computeHash(r);
    return r;
  };
  const r0 = mk(0, 'op', {
    patch: [{ op: 'set', key: 'a', value: 1 }],
    inverse: [{ op: 'del', key: 'a' }],
    txId: null,
  }, GENESIS);
  const r1 = mk(1, 'checkpoint', { label: null, stateDigest: 'forged', prefixDigest: r0.hash }, r0.hash);
  const engine = new RecoveryEngine();
  engine.load([r0, r1].map((r) => JSON.stringify(r)).join('\n') + '\n');
  engine.step();
  assert.throws(() => engine.step(), CheckpointSpliceError);
});

test('checkpoint restore resumes to an identical final digest; reclamation keeps the newest', () => {
  const b = buildLog();
  const serialized = b.serialize();
  const full = recover(serialized);

  const engine = new RecoveryEngine();
  engine.load(serialized);
  for (let i = 0; i < 6; i++) engine.step();
  engine.checkpoint('early');
  for (let i = 0; i < 6; i++) engine.step();
  const mid = engine.checkpoint('mid');
  engine.runToEnd();
  engine.checkpoint('late');

  assert.equal(engine.reclaimCheckpoints(), 2);
  assert.equal(engine.checkpointCount, 1);

  const resumed = new RecoveryEngine();
  resumed.load(serialized);
  resumed.restore(mid);
  resumed.runToEnd();
  assert.equal(resumed.digest(), full.digest());
  assert.equal(resumed.digest(), b.state.digest());
});

test('rollback applies inverse patches to any prefix and re-stepping reproduces the final digest', () => {
  const b = buildLog();
  const serialized = b.serialize();
  const records = b.getRecords();

  const forward = new RecoveryEngine();
  forward.load(serialized);
  const prefixDigests: string[] = [];
  for (let i = 0; i < records.length; i++) {
    forward.step();
    prefixDigests.push(forward.digest());
  }
  const finalDigest = forward.digest();

  for (let i = records.length - 1; i >= 0; i--) {
    forward.rollback();
    const expected = i === 0 ? new StateStore().digest() : prefixDigests[i - 1];
    assert.equal(forward.digest(), expected, `rollback to prefix ${i - 1} diverged`);
  }
  assert.deepEqual(forward.getState(), {});

  forward.runToEnd();
  assert.equal(forward.digest(), finalDigest);
  assert.equal(forward.digest(), b.state.digest());
});

test('cancel aborts recovery, blocks stepping until reset, and preserves baseline', () => {
  const b = buildLog();
  const engine = new RecoveryEngine();
  engine.load(b.serialize());
  for (let i = 0; i < 6; i++) engine.step();
  engine.cancel();
  assert.deepEqual(engine.getState(), {});
  assert.throws(() => engine.step(), RecoveryCancelledError);
  assert.throws(() => engine.rollback(), RecoveryCancelledError);
  engine.reset();
  engine.runToEnd();
  assert.equal(engine.digest(), b.state.digest());
});

test('confirmed side effects are never re-triggered during replay', () => {
  const originalCalls: string[] = [];
  const b = buildLog(originalCalls);
  assert.deepEqual(originalCalls, ['email-1']);

  const replayCalls: string[] = [];
  const engine = recover(b.serialize(), (id) => {
    replayCalls.push(id);
    return null;
  });
  assert.deepEqual(replayCalls, [], 'acked effect must not be re-executed');
  assert.deepEqual(engine.effectsExecutedByRecovery, []);
});

test('an effect without ack is executed exactly once during recovery', () => {
  const b = buildLog();
  const lines = b.serialize().trimEnd().split('\n');
  const effectSeq = lines.findIndex((l) => (JSON.parse(l) as LogRecord).type === 'effect');
  const truncated = lines.slice(0, effectSeq + 1).join('\n') + '\n';

  const calls: string[] = [];
  const engine = recover(truncated, (id) => {
    calls.push(id);
    return { retried: true };
  });
  assert.deepEqual(calls, ['email-1'], 'unacked effect must be executed exactly once');
  assert.deepEqual(engine.effectsExecutedByRecovery, ['email-1']);
});

test('final digest after recovery equals the original state digest', () => {
  const calls: string[] = [];
  const b = buildLog(calls);
  const engine = new RecoveryEngine();
  engine.load(b.serialize());
  while (!engine.step().done) { /* drain */ }
  assert.equal(engine.digest(), b.state.digest());
});
