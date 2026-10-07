/**
 * Model-based property test.
 *
 * The serial reference model decides the ground truth (which ops the
 * server accepts, and the final state). The client store is fed the same
 * ops plus the model's receipts delivered in random order, possibly
 * interleaved with submissions. After every single event the visible
 * state must match the reference model, and once all receipts are in,
 * the confirmed baseline must equal the serial model's final state.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { OptimisticStore, type Operation } from '../src/store.ts';
import {
  expectedVisibleState,
  mapsEqual,
  mulberry32,
  runSerialModel,
  shuffled,
  type SimulatedOp,
} from './reference.ts';

type V = Record<string, unknown>;

const TRIALS = Number(process.env.PROPERTY_TRIALS ?? 300);
const TARGETS = ['o0', 'o1', 'o2', 'o3', 'o4'];

function randomTrial(seed: number): { initial: Map<string, V>; simOps: SimulatedOp<V>[] } {
  const rand = mulberry32(seed);
  const initial = new Map<string, V>();
  for (const target of TARGETS) {
    if (rand() < 0.3) initial.set(target, { seed: Math.floor(rand() * 100) });
  }
  const count = 5 + Math.floor(rand() * 25);
  const simOps: SimulatedOp<V>[] = [];
  for (let i = 0; i < count; i++) {
    const target = TARGETS[Math.floor(rand() * TARGETS.length)];
    const roll = rand();
    const kind = roll < 0.4 ? 'create' : roll < 0.8 ? 'update' : 'delete';
    const op: Operation<V> =
      kind === 'delete'
        ? { id: `op${i}`, kind, target }
        : { id: `op${i}`, kind, target, value: { [`k${i}`]: Math.floor(rand() * 1000) } };
    // Server randomly rejects ~20% of ops (stand-in for validation errors).
    simOps.push({ op, randomlyRejected: rand() < 0.2 });
  }
  return { initial, simOps };
}

function assertStoreMatchesModel<V>(
  store: OptimisticStore<V>,
  initial: ReadonlyMap<string, V>,
  simOps: readonly SimulatedOp<V>[],
  rejectedSoFar: ReadonlySet<string>,
  context: string,
): void {
  const expected = expectedVisibleState(initial, simOps, rejectedSoFar);
  assert.ok(
    mapsEqual(store.state(), expected),
    `${context}: visible state mismatch\n` +
      `  actual:   ${JSON.stringify(Object.fromEntries(store.state()))}\n` +
      `  expected: ${JSON.stringify(Object.fromEntries(expected))}`,
  );
}

test('model: submit-all then out-of-order receipts', () => {
  for (let seed = 1; seed <= TRIALS; seed++) {
    const { initial, simOps } = randomTrial(seed);
    const model = runSerialModel(initial, simOps);
    const store = new OptimisticStore<V>(initial);
    const rand = mulberry32(seed * 7919);

    for (const { op } of simOps) store.submit(op);

    const rejectedSoFar = new Set<string>();
    const receipts = shuffled(model.receipts, rand);
    for (const receipt of receipts) {
      store.receiveReceipt(receipt.id, receipt.accepted);
      if (!receipt.accepted) rejectedSoFar.add(receipt.id);
      assertStoreMatchesModel(store, initial, simOps, rejectedSoFar, `seed=${seed} after ${receipt.id}`);
    }

    assert.ok(
      mapsEqual(store.baseline, model.state),
      `seed=${seed}: baseline must equal serial model state`,
    );
    assert.equal(store.pending.length, 0, `seed=${seed}: no pending ops left`);
    for (const { op } of simOps) {
      const status = store.statusOf(op.id);
      const expected = model.acceptedIds.has(op.id) ? 'committed' : 'rejected';
      assert.equal(status, expected, `seed=${seed}: ${op.id} should be ${expected}`);
    }
  }
});

test('model: submissions interleaved with out-of-order receipts', () => {
  for (let seed = 1; seed <= TRIALS; seed++) {
    const { initial, simOps } = randomTrial(seed);
    const model = runSerialModel(initial, simOps);
    const store = new OptimisticStore<V>(initial);
    const rand = mulberry32(seed * 104729);

    const rejectedSoFar = new Set<string>();
    const undelivered = new Map(model.receipts.map((r) => [r.id, r]));
    let submitted = 0;

    while (submitted < simOps.length || undelivered.size > 0) {
      const deliverable = simOps
        .slice(0, submitted)
        .filter(({ op }) => undelivered.has(op.id));
      const canSubmit = submitted < simOps.length;
      const doSubmit = canSubmit && (deliverable.length === 0 || rand() < 0.5);

      if (doSubmit) {
        store.submit(simOps[submitted].op);
        submitted++;
      } else {
        const pick = deliverable[Math.floor(rand() * deliverable.length)];
        const receipt = undelivered.get(pick.op.id)!;
        undelivered.delete(pick.op.id);
        store.receiveReceipt(receipt.id, receipt.accepted);
        if (!receipt.accepted) rejectedSoFar.add(receipt.id);
      }
      assertStoreMatchesModel(store, initial, simOps.slice(0, submitted), rejectedSoFar, `seed=${seed}`);
    }

    assert.ok(
      mapsEqual(store.baseline, model.state),
      `seed=${seed}: baseline must equal serial model state`,
    );
    assert.equal(store.pending.length, 0, `seed=${seed}: no pending ops left`);
  }
});
