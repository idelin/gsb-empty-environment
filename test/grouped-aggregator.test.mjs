import assert from 'node:assert/strict';
import { GroupedAggregator, AggregatorError } from '../dist/grouped-aggregator.js';

let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed += 1;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exit(1);
  }
}

function expectCode(code, fn) {
  assert.throws(fn, (err) => {
    assert.ok(err instanceof AggregatorError, `expected AggregatorError, got ${err}`);
    assert.equal(err.code, code);
    return true;
  });
}

// --- basic insert + aggregates ---
test('insert maintains count, sum, min, top-2', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'a', group: 'g1', value: 5 });
  agg.insert({ id: 'b', group: 'g1', value: 3 });
  agg.insert({ id: 'c', group: 'g1', value: 9 });
  agg.insert({ id: 'd', group: 'g1', value: 1 });
  const stats = agg.getGroupStats('g1');
  assert.equal(stats.count, 4);
  assert.equal(stats.sum, 18);
  assert.equal(stats.min, 1);
  assert.deepEqual(stats.top, [
    { value: 9, ids: ['c'] },
    { value: 5, ids: ['a'] },
  ]);
});

// --- retraction is exact, not an append-only summary ---
test('retract recomputes min and top-2 exactly (no monotonic summary)', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'lo', group: 'g', value: 1 });
  agg.insert({ id: 'hi', group: 'g', value: 100 });
  agg.insert({ id: 'mid', group: 'g', value: 50 });
  agg.retract('lo');
  assert.equal(agg.getGroupStats('g').min, 50); // min must rise after removing the minimum
  agg.retract('hi');
  const stats = agg.getGroupStats('g');
  assert.equal(stats.count, 1);
  assert.equal(stats.sum, 50);
  assert.deepEqual(stats.top, [{ value: 50, ids: ['mid'] }]);
});

// --- duplicate retraction is rejected ---
test('duplicate retract throws UNKNOWN_EVENT_ID', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'x', group: 'g', value: 1 });
  agg.retract('x');
  expectCode('UNKNOWN_EVENT_ID', () => agg.retract('x'));
  expectCode('UNKNOWN_EVENT_ID', () => agg.retract('never-seen'));
});

// --- duplicate insert id is rejected ---
test('duplicate insert id throws DUPLICATE_EVENT_ID', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'x', group: 'g', value: 1 });
  expectCode('DUPLICATE_EVENT_ID', () => agg.insert({ id: 'x', group: 'g', value: 2 }));
});

// --- empty groups are deleted ---
test('empty groups are deleted eagerly', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'x', group: 'g1', value: 1 });
  agg.insert({ id: 'y', group: 'g2', value: 2 });
  assert.equal(agg.groupCount, 2);
  agg.retract('x');
  assert.equal(agg.groupCount, 1);
  assert.equal(agg.hasGroup('g1'), false);
  assert.equal(agg.getGroupStats('g1'), undefined);
  assert.deepEqual(agg.listGroups(), ['g2']);
});

// --- tie ranking: dense ranks, ties share a rank ---
test('top-2 uses dense ranks and includes all tied ids', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'a', group: 'g', value: 10 });
  agg.insert({ id: 'b', group: 'g', value: 10 });
  agg.insert({ id: 'c', group: 'g', value: 7 });
  agg.insert({ id: 'd', group: 'g', value: 7 });
  agg.insert({ id: 'e', group: 'g', value: 3 });
  const { top } = agg.getGroupStats('g');
  assert.deepEqual(top, [
    { value: 10, ids: ['a', 'b'] },
    { value: 7, ids: ['c', 'd'] },
  ]);
  // Retracting one tied event keeps the rank alive for the other.
  agg.retract('a');
  assert.deepEqual(agg.getGroupStats('g').top[0], { value: 10, ids: ['b'] });
  // Retracting the last tied event drops the rank; the next rank moves up.
  agg.retract('b');
  assert.deepEqual(agg.getGroupStats('g').top, [
    { value: 7, ids: ['c', 'd'] },
    { value: 3, ids: ['e'] },
  ]);
});

// --- atomic group change: happy path ---
test('moveToGroup moves contribution between groups atomically', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'x', group: 'src', value: 5 });
  agg.insert({ id: 'y', group: 'dst', value: 8 });
  agg.moveToGroup('x', 'dst');
  assert.equal(agg.hasGroup('src'), false); // source emptied -> deleted
  const dst = agg.getGroupStats('dst');
  assert.equal(dst.count, 2);
  assert.equal(dst.sum, 13);
  assert.equal(dst.min, 5);
  assert.deepEqual(dst.top, [
    { value: 8, ids: ['y'] },
    { value: 5, ids: ['x'] },
  ]);
});

// --- atomic group change: failure leaves state untouched ---
test('failed group change is atomic: no partial mutation', () => {
  const agg = new GroupedAggregator({ maxEventsPerGroup: 1 });
  agg.insert({ id: 'x', group: 'src', value: 5 });
  agg.insert({ id: 'y', group: 'dst', value: 8 });
  expectCode('GROUP_CAPACITY_EXCEEDED', () => agg.moveToGroup('x', 'dst'));
  // Source group still intact, event still there, destination unchanged.
  assert.deepEqual(agg.getGroupStats('src'), {
    group: 'src', count: 1, sum: 5, min: 5, top: [{ value: 5, ids: ['x'] }],
  });
  assert.equal(agg.getGroupStats('dst').count, 1);
  assert.equal(agg.hasEvent('x'), true);
  assert.equal(agg.eventCount, 2);
});

// --- bounded index: maxGroups ---
test('bounded index rejects groups beyond maxGroups', () => {
  const agg = new GroupedAggregator({ maxGroups: 2 });
  agg.insert({ id: 'a', group: 'g1', value: 1 });
  agg.insert({ id: 'b', group: 'g2', value: 2 });
  expectCode('GROUP_LIMIT_EXCEEDED', () => agg.insert({ id: 'c', group: 'g3', value: 3 }));
  // Moving into a brand-new group is also bounded, and the failure is atomic.
  expectCode('GROUP_LIMIT_EXCEEDED', () => agg.moveToGroup('a', 'g3'));
  assert.deepEqual(agg.getGroupStats('g1').top, [{ value: 1, ids: ['a'] }]);
  // A freed slot (empty group deleted) can be reused.
  agg.retract('b');
  agg.insert({ id: 'c', group: 'g3', value: 3 });
  assert.deepEqual(agg.listGroups(), ['g1', 'g3']);
});

// --- bounded index: maxEventsPerGroup ---
test('bounded index rejects inserts beyond per-group capacity', () => {
  const agg = new GroupedAggregator({ maxEventsPerGroup: 2 });
  agg.insert({ id: 'a', group: 'g', value: 1 });
  agg.insert({ id: 'b', group: 'g', value: 2 });
  expectCode('GROUP_CAPACITY_EXCEEDED', () => agg.insert({ id: 'c', group: 'g', value: 3 }));
  agg.retract('a');
  agg.insert({ id: 'c', group: 'g', value: 3 });
  assert.equal(agg.getGroupStats('g').count, 2);
});

// --- update value within the same group ---
test('update changes value in place and refreshes aggregates', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'a', group: 'g', value: 1 });
  agg.insert({ id: 'b', group: 'g', value: 2 });
  agg.update('a', { value: 10 });
  const stats = agg.getGroupStats('g');
  assert.equal(stats.sum, 12);
  assert.equal(stats.min, 2);
  assert.deepEqual(stats.top, [
    { value: 10, ids: ['a'] },
    { value: 2, ids: ['b'] },
  ]);
});

// --- update validates input and unknown ids ---
test('update rejects unknown ids and non-finite values without mutating', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'a', group: 'g', value: 1 });
  expectCode('UNKNOWN_EVENT_ID', () => agg.update('ghost', { value: 5 }));
  expectCode('INVALID_VALUE', () => agg.update('a', { value: NaN }));
  expectCode('INVALID_VALUE', () => agg.insert({ id: 'b', group: 'g', value: Infinity }));
  assert.equal(agg.getGroupStats('g').sum, 1);
});

// --- combined group+value change in one atomic update ---
test('update can change group and value together', () => {
  const agg = new GroupedAggregator();
  agg.insert({ id: 'a', group: 'g1', value: 1 });
  agg.update('a', { group: 'g2', value: 42 });
  assert.equal(agg.hasGroup('g1'), false);
  assert.deepEqual(agg.getGroupStats('g2'), {
    group: 'g2', count: 1, sum: 42, min: 42, top: [{ value: 42, ids: ['a'] }],
  });
});

console.log(`\n${passed} tests passed`);
