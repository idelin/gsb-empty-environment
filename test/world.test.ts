import { World } from '../src/world.js';
import { Fraction } from '../src/fraction.js';
import { test, assert } from './harness.js';

function mkWorld () {
  const w = new World();
  w.upsert({ id: 'a', x: 0, y: 0, w: 1, h: 1, vx: 0, vy: 0 });
  w.upsert({ id: 'b', x: 2, y: 0, w: 1, h: 1, vx: -1, vy: 0 });
  return w;
}

test('world: updates are batched and invisible before commit', () => {
  const w = mkWorld();
  assert.strictEqual(w.bodyCount(), 0);
  assert.strictEqual(w.query(3, undefined).exactPairs, 0);
  w.commit();
  assert.strictEqual(w.bodyCount(), 2);
  const r = w.query(3, undefined);
  assert.strictEqual(r.collisions.length, 1);
  assert.strictEqual(r.collisions[0].start.toString(), '1');
});

test('world: deleted body cannot resurrect from stale grid cells', () => {
  const w = mkWorld();
  w.commit();
  assert.strictEqual(w.query(3, undefined).exactPairs, 1);
  w.remove('b');
  w.commit();
  const r = w.query(3, undefined);
  assert.strictEqual(r.exactPairs, 0);
  assert.strictEqual(r.candidatePairs, 0);
  assert.strictEqual(r.verified, true);
});

test('world: stable id cannot be reused after deletion', () => {
  const w = mkWorld();
  w.commit();
  w.remove('b');
  w.commit();
  assert.throws(() => {
    w.upsert({ id: 'b', x: 0, y: 0, w: 1, h: 1, vx: 0, vy: 0 });
    w.commit();
  });
  // delete + re-add inside a single batch must also fail
  const w2 = mkWorld();
  w2.commit();
  assert.throws(() => {
    w2.remove('a');
    w2.upsert({ id: 'a', x: 9, y: 9, w: 1, h: 1, vx: 0, vy: 0 });
    w2.commit();
  });
});

test('world: upsert of an existing id updates in place', () => {
  const w = mkWorld();
  w.commit();
  w.upsert({ id: 'b', x: 10, y: 10, w: 1, h: 1, vx: 0, vy: 0 });
  w.commit();
  assert.strictEqual(w.bodyCount(), 2);
  assert.strictEqual(w.query(3, undefined).exactPairs, 0);
});

test('world: each colliding pair is reported exactly once', () => {
  const w = new World();
  // three mutually overlapping bodies on grid boundaries, tiny cells
  w.upsert({ id: 'a', x: 0, y: 0, w: 2, h: 2, vx: 0, vy: 0 });
  w.upsert({ id: 'b', x: 1, y: 1, w: 2, h: 2, vx: 0, vy: 0 });
  w.upsert({ id: 'c', x: '1/2', y: '1/2', w: 1, h: 1, vx: 0, vy: 0 });
  w.commit();
  const r = w.query(1, '1/8');
  assert.strictEqual(r.collisions.length, 3);
  const keys = r.collisions.map(c => [c.a, c.b].join('|'));
  assert.strictEqual(new Set(keys).size, 3);
});

test('world: candidate count and exact count are reported and consistent', () => {
  const w = mkWorld();
  w.commit();
  const r = w.query(3, undefined);
  assert.strictEqual(r.candidatePairs, 1);
  assert.strictEqual(r.exactPairs, 1);
  assert.strictEqual(r.verified, true);
  assert.deepStrictEqual(r.missedPairs, []);
});

test('world: randomized scene, broad phase never misses a pair', () => {
  // deterministic LCG so the test is reproducible
  let state = 987654321n;
  const next = () => {
    state = (state * 6364136223846793005n + 1442695040888963407n) % 4611686018427387904n;
    return state / 1099511627776n; // 2^40
  };
  const frac = (span: number, den: bigint) => {
    const n = BigInt(Math.floor(Number(next()) * span)) - BigInt(span / 2);
    return Fraction.fromBigInt(n, den);
  };
  const fracPos = (span: number, den: bigint) => {
    const n = BigInt(Math.floor(Number(next()) * span));
    return Fraction.fromBigInt(n, den);
  };
  const w = new World();
  for (let i = 0; i < 40; i++) {
    w.upsert({
      id: 'o' + i,
      x: frac(40, 7n), y: frac(40, 7n),
      w: fracPos(6, 3n), h: fracPos(6, 3n),
      vx: frac(8, 5n), vy: frac(8, 5n),
    });
  }
  w.commit();
  const r = w.query('5/2', undefined);
  assert.strictEqual(r.verified, true);
  assert.strictEqual(r.collisions.length, r.exactPairs);
  assert.strictEqual(r.candidatePairs >= r.exactPairs, true);
  assert.strictEqual(r.candidatePairs <= (40 * 39) / 2, true);
});

test('world: empty world queries cleanly', () => {
  const w = new World();
  w.commit();
  const r = w.query(1, undefined);
  assert.strictEqual(r.exactPairs, 0);
  assert.strictEqual(r.candidatePairs, 0);
  assert.strictEqual(r.verified, true);
});
