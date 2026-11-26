import test from 'node:test';
import assert from 'node:assert/strict';
import { ModRing } from '../src/ring.ts';
import { fieldRankModP, isConsistentSystem, smithNormalForm } from '../src/smith.ts';

function decodeMatrix(flat: number, q: number): number[][] {
  const values = [0, 0, 0, 0];
  let remaining = flat;
  for (let i = 0; i < 4; i += 1) {
    values[i] = remaining % q;
    remaining = Math.floor(remaining / q);
  }
  return [
    [values[0]!, values[1]!],
    [values[2]!, values[3]!],
  ];
}

function bruteKernelSize(matrix: number[][], ring: ModRing): number {
  let count = 0;
  for (let first = 0; first < ring.q; first += 1) {
    for (let second = 0; second < ring.q; second += 1) {
      const vector = [first, second];
      if (matrix.every((row) => ring.dot(row, vector) === 0)) count += 1;
    }
  }
  return count;
}

function generatedKernelSize(matrix: number[][], ring: ModRing): {
  count: number;
  allInKernel: boolean;
} {
  const snf = smithNormalForm(matrix, ring);
  const orders = snf.generatorOrders.map((order) => Number(order));
  const vectors = new Set<string>();
  let allInKernel = true;
  const total = orders.reduce((product, order) => product * order, 1);

  for (let flat = 0; flat < total; flat += 1) {
    let remaining = flat;
    const vector = new Array<number>(2).fill(0);
    for (let generatorIndex = 0; generatorIndex < snf.kernelGenerators.length; generatorIndex += 1) {
      const order = orders[generatorIndex]!;
      const coefficient = remaining % order;
      remaining = Math.floor(remaining / order);
      const generator = snf.kernelGenerators[generatorIndex]!;
      for (let row = 0; row < 2; row += 1) {
        vector[row] = ring.add(vector[row]!, ring.mul(coefficient, generator[row]!));
      }
    }
    vectors.add(vector.join(','));
    if (!matrix.every((row) => ring.dot(row, vector) === 0)) allInKernel = false;
  }

  return { count: vectors.size, allInKernel };
}

for (const modulus of [4, 9, 8]) {
  test(`Smith kernel matches exhaustive enumeration over Z/${modulus}Z`, () => {
    const ring = new ModRing(modulus);
    const matrixCount = ring.q ** 4;
    for (let flat = 0; flat < matrixCount; flat += 1) {
      const matrix = decodeMatrix(flat, ring.q);
      const snf = smithNormalForm(matrix, ring);
      const brute = bruteKernelSize(matrix, ring);
      assert.equal(snf.kernelSize, BigInt(brute), `kernel size failed for ${JSON.stringify(matrix)}`);

      const generated = generatedKernelSize(matrix, ring);
      assert.equal(generated.count, brute, `generator count failed for ${JSON.stringify(matrix)}`);
      assert.equal(generated.allInKernel, true);
    }
  });
}

test('non-unit multiplier reports partial modulo-p leakage instead of field rank', () => {
  for (const [p, e] of [
    [2, 2],
    [3, 2],
    [2, 3],
  ] as const) {
    const ring = new ModRing({ p, e });
    const coefficient = p ** (e - 1);
    const snf = smithNormalForm([[coefficient]], ring);
    assert.equal(snf.rank, 1);
    assert.deepEqual(snf.exponents, [e - 1]);
    assert.equal(snf.imageSize, BigInt(p));
    assert.equal(snf.kernelSize, BigInt(p ** (e - 1)));
    assert.equal(fieldRankModP([[coefficient]], ring), e === 1 ? 1 : 0);
  }
});

test('SNF consistency agrees with brute-force solutions for one-row systems', () => {
  for (const modulus of [4, 9, 8]) {
    const ring = new ModRing(modulus);
    for (let a = 0; a < ring.q; a += 1) {
      for (let b = 0; b < ring.q; b += 1) {
        for (let target = 0; target < ring.q; target += 1) {
          let bruteConsistent = false;
          for (let x = 0; x < ring.q && !bruteConsistent; x += 1) {
            for (let y = 0; y < ring.q; y += 1) {
              if (ring.add(ring.mul(a, x), ring.mul(b, y)) === target) {
                bruteConsistent = true;
                break;
              }
            }
          }
          assert.equal(
            isConsistentSystem([[a, b]], [target], ring),
            bruteConsistent,
            `a=${a} b=${b} target=${target} q=${modulus}`,
          );
        }
      }
    }
  }
});

test('SNF consistency agrees with brute-force multi-row systems', () => {
  for (const modulus of [4, 9, 8]) {
    const ring = new ModRing(modulus);
    let seed = 987654321;
    const random = () => {
      seed = (Math.imul(seed, 1103515245) + 12345) >>> 0;
      return seed / 4294967296;
    };

    for (let iteration = 0; iteration < 120; iteration += 1) {
      const rows = 1 + Math.floor(random() * 3);
      const columns = 1 + Math.floor(random() * 3);
      const matrix = Array.from({ length: rows }, () =>
        Array.from({ length: columns }, () => Math.floor(random() * ring.q)),
      );
      const target = Array.from({ length: rows }, () => Math.floor(random() * ring.q));

      let bruteConsistent = false;
      for (let flat = 0; flat < ring.q ** columns && !bruteConsistent; flat += 1) {
        let remaining = flat;
        const values = Array.from({ length: columns }, () => {
          const value = remaining % ring.q;
          remaining = Math.floor(remaining / ring.q);
          return value;
        });
        bruteConsistent = matrix.every((row, rowIndex) => ring.dot(row, values) === target[rowIndex]);
      }

      assert.equal(isConsistentSystem(matrix, target, ring), bruteConsistent);
    }
  }
});
