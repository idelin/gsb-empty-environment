import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DeclarationError,
  FixpointEngine,
  NonMonotoneError,
  type SchedulerKind,
} from '../src/engine.ts';
import { booleanLattice, setLattice } from '../src/lattice.ts';

function buildReachability(scheduler?: SchedulerKind): FixpointEngine {
  const engine = new FixpointEngine(scheduler === undefined ? {} : { scheduler });
  engine.input('s', booleanLattice, true);
  engine.derived('a', booleanLattice, ['s', 'c'], ([s, c]) => Boolean(s) || Boolean(c), {
    monotone: true,
  });
  engine.derived('b', booleanLattice, ['a'], ([a]) => Boolean(a), { monotone: true });
  engine.derived('c', booleanLattice, ['b'], ([b]) => Boolean(b), { monotone: true });
  engine.derived('d', booleanLattice, [], () => false, { monotone: true });
  return engine;
}

test('无环链收敛', () => {
  const engine = new FixpointEngine();
  engine.input('x', booleanLattice, true);
  engine.derived('y', booleanLattice, ['x'], ([x]) => Boolean(x), { monotone: true });
  engine.derived('z', booleanLattice, ['y'], ([y]) => Boolean(y), { monotone: true });
  const result = engine.settle();
  assert.equal(result.status, 'converged');
  assert.equal(result.values.get('z'), true);
});

test('单调环（布尔可达闭包）收敛到最小不动点', () => {
  const result = buildReachability().settle();
  assert.equal(result.status, 'converged');
  assert.deepEqual(
    Object.fromEntries(result.values),
    { s: true, a: true, b: true, c: true, d: false },
  );
});

test('收敛过程中值沿格序单调不减', () => {
  const engine = buildReachability();
  engine.settle();
  for (const name of ['s', 'a', 'b', 'c', 'd']) {
    const history = engine.historyOf(name) as boolean[];
    for (let i = 1; i < history.length; i++) {
      assert.ok(
        booleanLattice.leq(history[i - 1], history[i]),
        `${name} 的历史 ${history} 在 ${i} 处下降`,
      );
    }
  }
});

test('x = NOT x 被拒绝，且不无限振荡', () => {
  const engine = new FixpointEngine();
  engine.derived('x', booleanLattice, ['x'], ([x]) => !x, { monotone: true });
  let error: NonMonotoneError | undefined;
  try {
    engine.settle();
  } catch (caught) {
    error = caught as NonMonotoneError;
  }
  assert.ok(error instanceof NonMonotoneError, '应抛出 NonMonotoneError');
  assert.equal(error.node, 'x');
  assert.ok(error.iterations < 10, `应在有限步内拒绝，实际 ${error.iterations}`);
});

test('未声明单调的派生节点在声明期被拒绝', () => {
  const engine = new FixpointEngine();
  assert.throws(
    () => engine.derived('x', booleanLattice, [], () => true, { monotone: false }),
    DeclarationError,
  );
});

test('依赖不存在的节点在 settle 时被拒绝', () => {
  const engine = new FixpointEngine();
  engine.derived('x', booleanLattice, ['ghost'], () => true, { monotone: true });
  assert.throws(() => engine.settle(), DeclarationError);
});

test('迭代预算耗尽标记 non-converged', () => {
  const engine = new FixpointEngine({ budget: 10 });
  engine.input('n0', booleanLattice, true);
  for (let i = 1; i <= 50; i++) {
    engine.derived(`n${i}`, booleanLattice, [`n${i - 1}`], ([prev]) => Boolean(prev), {
      monotone: true,
    });
  }
  const result = engine.settle();
  assert.equal(result.status, 'non-converged');
  assert.equal(result.iterations, 10);
});

test('预算充足时同一链收敛', () => {
  const engine = new FixpointEngine({ budget: 1000 });
  engine.input('n0', booleanLattice, true);
  for (let i = 1; i <= 50; i++) {
    engine.derived(`n${i}`, booleanLattice, [`n${i - 1}`], ([prev]) => Boolean(prev), {
      monotone: true,
    });
  }
  assert.equal(engine.settle().status, 'converged');
});

test('调度顺序不同仍得同一最小不动点', () => {
  const snapshots: string[] = [];
  for (const scheduler of ['fifo', 'lifo', 'priority'] as const) {
    const result = buildReachability(scheduler).settle();
    assert.equal(result.status, 'converged');
    snapshots.push(JSON.stringify([...result.values].sort()));
  }
  assert.equal(new Set(snapshots).size, 1);
});

test('输入节点 join 语义：只升不降', () => {
  const engine = new FixpointEngine();
  engine.input('x', booleanLattice, true);
  engine.setInput('x', false);
  assert.equal(engine.getValue('x'), true);
  engine.setInput('x', true);
  assert.equal(engine.getValue('x'), true);
});

test('写派生节点被拒绝', () => {
  const engine = new FixpointEngine();
  engine.derived('y', booleanLattice, [], () => false, { monotone: true });
  assert.throws(() => engine.setInput('y', true), DeclarationError);
});

test('幂集格上的单调闭包收敛', () => {
  const lat = setLattice<number>();
  const engine = new FixpointEngine();
  engine.input('seed', lat, new Set([1]));
  // f(S) = S ∪ {x+1 | x ∈ S, x < 4}，单调，闭包为 {1,2,3,4}
  engine.derived(
    'closure',
    lat,
    ['seed', 'closure'],
    ([seed, self]) => {
      const out = new Set(seed as ReadonlySet<number>);
      for (const x of self as ReadonlySet<number>) if (x < 4) out.add(x + 1);
      return out;
    },
    { monotone: true },
  );
  const result = engine.settle();
  assert.equal(result.status, 'converged');
  assert.deepEqual(result.values.get('closure'), new Set([1, 2, 3, 4]));
});
