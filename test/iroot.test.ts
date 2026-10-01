import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isqrt, iroot, irootGeneric, bitLength, powSmall } from '../src/iroot.ts';

// deterministic PRNG (splitmix64-style) for reproducible "random" bigints
function makeRng(seed: bigint) {
  let state = seed;
  return (bits: number): bigint => {
    let out = 0n;
    let got = 0;
    while (got < bits) {
      state = (state + 0x9e3779b97f4a7c15n) & 0xffffffffffffffffn;
      let z = state;
      z = ((z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n) & 0xffffffffffffffffn;
      z = ((z ^ (z >> 27n)) * 0x94d049bb133111ebn) & 0xffffffffffffffffn;
      z ^= z >> 31n;
      out = (out << 64n) | z;
      got += 64;
    }
    return out & ((1n << BigInt(bits)) - 1n);
  };
}

test('exhaustive small isqrt 0..10000: floor property + remainder', () => {
  for (let t = 0n; t <= 10000n; t++) {
    const { root, remainder, exact } = isqrt(t);
    assert.ok(root * root <= t, `root^2 <= n failed at ${t}`);
    assert.ok((root + 1n) * (root + 1n) > t, `(root+1)^2 > n failed at ${t}`);
    assert.equal(remainder, t - root * root);
    assert.equal(exact, remainder === 0n);
  }
});

test('exhaustive small iroot k=3..6, 0..3000', () => {
  for (let k = 3; k <= 6; k++) {
    for (let t = 0n; t <= 3000n; t++) {
      const { root, remainder, exact } = iroot(k, t);
      assert.ok(powSmall(root, k) <= t, `k=${k} t=${t}`);
      assert.ok(powSmall(root + 1n, k) > t, `k=${k} t=${t}`);
      assert.equal(remainder, t - powSmall(root, k));
      assert.equal(exact, remainder === 0n);
    }
  }
});

test('square boundary counterexamples: r^2-1, r^2, r^2+1, (r+1)^2-1', () => {
  const rs = [
    1n, 2n, 3n, 255n, 2n ** 26n, 2n ** 32n - 1n, 2n ** 53n,
    10n ** 20n, 123456789123456789123456789n, 2n ** 200n + 12345n,
  ];
  for (const r of rs) {
    const sq = r * r;
    assert.equal(isqrt(sq - 1n).root, r - 1n, `r^2-1 for r=${r}`);
    const hit = isqrt(sq);
    assert.equal(hit.root, r);
    assert.equal(hit.exact, true);
    assert.equal(hit.remainder, 0n);
    assert.equal(isqrt(sq + 1n).root, r, `r^2+1 for r=${r}`);
    assert.equal(isqrt(sq + 2n * r).root, r, `(r+1)^2-1 for r=${r}`);
  }
});

test('nth-power boundary counterexamples for k = 3, 5, 7', () => {
  const rs = [2n, 10n, 999n, 2n ** 40n, 987654321987654321n];
  for (const k of [3, 5, 7]) {
    for (const r of rs) {
      const p = powSmall(r, k);
      assert.equal(iroot(k, p - 1n).root, r - 1n);
      assert.equal(iroot(k, p).root, r);
      assert.equal(iroot(k, p).exact, true);
      assert.equal(iroot(k, p + 1n).root, r);
      assert.equal(iroot(k, powSmall(r + 1n, k) - 1n).root, r);
    }
  }
});

test('sqrt fast path matches generic Newton on random + adversarial inputs', () => {
  const rng = makeRng(0xdeadbeefn);
  const cases: bigint[] = [];
  for (let i = 0; i < 300; i++) cases.push(rng(1 + (i % 256)));
  for (let b = 2; b <= 130; b++) {
    cases.push((1n << BigInt(b)) - 1n); // Mersenne-like, worst case for naive loops
    cases.push(1n << BigInt(b));
  }
  for (const t of cases) {
    const fast = isqrt(t);
    const gen = irootGeneric(2, t);
    assert.equal(fast.root, gen.root, `mismatch at ${t}`);
    assert.equal(fast.remainder, gen.remainder);
  }
});

test('provable iteration bound holds: isqrt <= ceil(b/2)+2, iroot <= b+k+4', () => {
  const rng = makeRng(0x1234567n);
  for (let i = 0; i < 400; i++) {
    const t = rng(2 + (i % 200));
    if (t < 2n) continue;
    const b = bitLength(t);
    const s = isqrt(t);
    assert.ok(s.iterations <= Math.ceil(b / 2) + 2, `isqrt iterations ${s.iterations} for b=${b}`);
    for (const k of [3, 4, 5, 8, 13]) {
      const r = iroot(k, t);
      assert.ok(r.iterations <= b + k + 4, `iroot k=${k} iterations ${r.iterations} for b=${b}`);
    }
  }
});

test('negative input rejected; bad degree rejected', () => {
  assert.throws(() => isqrt(-1n), RangeError);
  assert.throws(() => iroot(3, -8n), RangeError);
  assert.throws(() => iroot(0, 5n), RangeError);
  assert.throws(() => iroot(-2, 5n), RangeError);
  assert.throws(() => iroot(2.5, 5n), RangeError);
});

test('edge cases: 0, 1, degree 1, degree larger than bitlength', () => {
  assert.deepEqual(isqrt(0n).root, 0n);
  assert.deepEqual(isqrt(1n).root, 1n);
  assert.equal(iroot(1, 12345678901234567890n).root, 12345678901234567890n);
  assert.equal(iroot(1000, 2n).root, 1n); // 2^(1/1000) floors to 1
  assert.equal(iroot(1000, 1n).root, 1n);
});
