import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FixpointEngine } from '../src/engine.ts';
import { booleanLattice } from '../src/lattice.ts';
import { Store } from '../src/store.ts';

function buildEngine(): FixpointEngine {
  const engine = new FixpointEngine();
  engine.input('s1', booleanLattice);
  engine.input('s2', booleanLattice);
  engine.derived('a', booleanLattice, ['s1'], ([s1]) => Boolean(s1), { monotone: true });
  engine.derived('b', booleanLattice, ['a', 's2'], ([a, s2]) => Boolean(a) || Boolean(s2), {
    monotone: true,
  });
  engine.derived('c', booleanLattice, ['b'], ([b]) => Boolean(b), { monotone: true });
  return engine;
}

test('读者看不到事务中间态', () => {
  const seenDuringSettle: unknown[] = [];
  const engine = new FixpointEngine();
  engine.input('src', booleanLattice);
  let store: Store | undefined;
  engine.derived('probe', booleanLattice, ['src'], ([src]) => {
    // 在 settle 期间读存储：只能看到提交前的快照
    if (store !== undefined) seenDuringSettle.push(store.read('probe'));
    return Boolean(src);
  }, { monotone: true });
  store = new Store(engine, () => engine);
  store.commit([{ type: 'set', cell: 'src', value: true }]);
  assert.ok(seenDuringSettle.length > 0);
  for (const seen of seenDuringSettle) {
    assert.equal(seen, false, 'settle 期间读者只能看到提交前的值');
  }
  assert.equal(store.read('probe'), true);
});

test('事务原子提交：一次版本跳动，全部写同时可见', () => {
  const store = new Store(buildEngine(), buildEngine);
  assert.equal(store.version, 0);
  assert.equal(store.read('a'), false);

  const tx = store.begin();
  tx.set('s1', true).set('s2', true);
  // 提交前：缓冲中的写不可见
  assert.equal(store.read('a'), false);
  assert.equal(store.read('b'), false);
  tx.commit();

  assert.equal(store.version, 1);
  assert.equal(store.read('a'), true);
  assert.equal(store.read('b'), true);
  assert.equal(store.read('c'), true);
  assert.equal(store.log.length, 1);
  assert.equal(store.log[0].ops.length, 2);
});

test('操作日志可回放：重放后状态一致', () => {
  const store = new Store(buildEngine(), buildEngine);
  store.begin().set('s1', true).commit();
  store.begin().set('s2', true).commit();

  const replayed = store.replay();
  const live = JSON.stringify([...store.snapshot()].sort());
  const redo = JSON.stringify([...replayed].sort());
  assert.equal(redo, live);
});

test('日志按提交顺序记录事务', () => {
  const store = new Store(buildEngine(), buildEngine);
  store.begin().set('s1', true).commit();
  store.begin().set('s2', true).commit();
  assert.deepEqual(
    store.log.map((entry) => entry.tx),
    [1, 2],
  );
  assert.equal(store.log[0].ops[0].cell, 's1');
  assert.equal(store.log[1].ops[0].cell, 's2');
});

test('事务关闭后不可再写或重复提交', () => {
  const store = new Store(buildEngine(), buildEngine);
  const tx = store.begin();
  tx.set('s1', true);
  tx.commit();
  assert.throws(() => tx.set('s2', true), /closed/);
  assert.throws(() => tx.commit(), /closed/);
});
