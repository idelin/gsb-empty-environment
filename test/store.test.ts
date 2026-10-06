import { Operation } from '../src/operation';
import { OptimisticStore } from '../src/store';
import {
  assert,
  assertEqual,
  assertStateEqual,
  test,
} from './harness';

const create = (opId: string, target: string, value: number): Operation => ({
  opId,
  kind: 'create',
  target,
  value,
});
const update = (opId: string, target: string, value: number): Operation => ({
  opId,
  kind: 'update',
  target,
  value,
});
const del = (opId: string, target: string): Operation => ({
  opId,
  kind: 'delete',
  target,
});

test('pending ops apply optimistically on the baseline', () => {
  const store = new OptimisticStore([['seed', 1]]);
  store.submit(create('o1', 'a', 10));
  store.submit(update('o2', 'a', 11));
  assertStateEqual(
    store.getState(),
    new Map([['seed', 1], ['a', 11]]),
    'optimistic view',
  );
});

test('out-of-order accept receipts converge to submission-order state', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'a', 1));
  store.submit(create('o2', 'b', 2));
  store.submit(update('o3', 'a', 100));

  // Receipts arrive in reverse order.
  store.handleReceipt({ opId: 'o3', accepted: true });
  assertStateEqual(store.getState(), new Map([['a', 100], ['b', 2]]), 'after o3');
  store.handleReceipt({ opId: 'o1', accepted: true });
  assertStateEqual(store.getState(), new Map([['a', 100], ['b', 2]]), 'after o1');
  store.handleReceipt({ opId: 'o2', accepted: true });
  assertStateEqual(store.getState(), new Map([['a', 100], ['b', 2]]), 'after o2');
  assertEqual(store.statusOf('o1'), 'confirmed', 'o1 status');
});

test('rejection removes the op and replays successors; siblings survive', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'a', 1)); // will be rejected
  store.submit(create('o2', 'b', 2)); // sibling, must survive
  store.submit(update('o3', 'a', 50)); // depends on o1 -> dependency failure
  store.submit(update('o4', 'b', 20)); // depends on o2 -> must survive

  assertStateEqual(
    store.getState(),
    new Map([['a', 50], ['b', 20]]),
    'before receipt',
  );

  store.handleReceipt({ opId: 'o1', accepted: false });

  // Not restoring the pre-o1 snapshot: o2/o4 effects are still visible.
  assertStateEqual(store.getState(), new Map([['b', 20]]), 'after reject o1');
  assertEqual(store.statusOf('o1'), 'rejected', 'o1 rejected');
  assertEqual(store.statusOf('o3'), 'failed', 'o3 dependency-failed');
  assertEqual(store.getFailures().get('o3'), 'dependency', 'o3 reason');
  assertEqual(store.statusOf('o4'), 'pending', 'o4 still live');
});

test('dependency failure recovers when the blocking op is rejected', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'x', 1));
  store.submit(del('o2', 'x'));
  store.submit(update('o3', 'x', 9)); // x deleted by o2 -> dependency failure

  assertEqual(store.getFailures().get('o3'), 'dependency', 'o3 blocked by o2');
  assertStateEqual(store.getState(), new Map(), 'x deleted optimistically');

  // Server rejects the delete: x lives again and o3 becomes applicable.
  store.handleReceipt({ opId: 'o2', accepted: false });
  assert(!store.getFailures().has('o3'), 'o3 recovered');
  assertStateEqual(store.getState(), new Map([['x', 9]]), 'o3 now applies');
});

test('rejected op never resurrected; later receipts still apply', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'a', 1));
  store.submit(create('o2', 'b', 2));
  store.handleReceipt({ opId: 'o1', accepted: false });
  store.handleReceipt({ opId: 'o2', accepted: true });
  assertStateEqual(store.getState(), new Map([['b', 2]]), 'final state');
  assertEqual(store.statusOf('o2'), 'confirmed', 'o2 confirmed');
});

test('unknown or duplicate receipts are rejected', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'a', 1));
  let threw = 0;
  try {
    store.handleReceipt({ opId: 'nope', accepted: true });
  } catch {
    threw += 1;
  }
  store.handleReceipt({ opId: 'o1', accepted: true });
  try {
    store.handleReceipt({ opId: 'o1', accepted: true });
  } catch {
    threw += 1;
  }
  assertEqual(threw, 2, 'both bad receipts threw');
});

test('duplicate opId submission is rejected', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'a', 1));
  let threw = false;
  try {
    store.submit(create('o1', 'b', 2));
  } catch {
    threw = true;
  }
  assert(threw, 'duplicate opId threw');
});

test('create on an existing object is a conflict failure', () => {
  const store = new OptimisticStore([['a', 1]]);
  store.submit(create('o1', 'a', 5));
  assertEqual(store.getFailures().get('o1'), 'conflict', 'conflict reason');
  assertStateEqual(store.getState(), new Map([['a', 1]]), 'state untouched');
});

test('baseline reflects confirmed ops only, in submission order', () => {
  const store = new OptimisticStore();
  store.submit(create('o1', 'a', 1));
  store.submit(create('o2', 'b', 2));
  store.submit(update('o3', 'a', 100));

  // o3 confirmed before o1: baseline cannot apply the update yet because
  // the create it targets is still unacknowledged.
  store.handleReceipt({ opId: 'o3', accepted: true });
  assertStateEqual(store.getBaseline(), new Map(), 'baseline lags on o3');
  assertStateEqual(
    store.getState(),
    new Map([['a', 100], ['b', 2]]),
    'optimistic view still correct',
  );

  store.handleReceipt({ opId: 'o1', accepted: true });
  assertStateEqual(store.getBaseline(), new Map([['a', 100]]), 'baseline caught up');
});
