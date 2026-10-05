import { describe, it, expect } from 'bun:test';
import {
  AssignmentSolver,
  verifyCertificate,
  type AssignmentResult,
} from '../src/assignment';

// ---------- deterministic PRNG ----------
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- brute force over all permutations ----------
function bruteForce(
  n: number,
  allowed: (i: number, j: number) => boolean,
  cost: (i: number, j: number) => bigint,
): { cost: bigint; perm: number[] } | null {
  let best: { cost: bigint; perm: number[] } | null = null;
  const perm = new Array<number>(n).fill(-1);
  const usedCol = new Array<boolean>(n).fill(false);
  const walk = (row: number, acc: bigint): void => {
    if (row === n) {
      if (best === null || acc < best.cost) best = { cost: acc, perm: [...perm] };
      return;
    }
    for (let j = 0; j < n; j++) {
      if (usedCol[j] || !allowed(row, j)) continue;
      usedCol[j] = true;
      perm[row] = j;
      walk(row + 1, acc + cost(row, j));
      usedCol[j] = false;
    }
  };
  walk(0, 0n);
  return best;
}

function randomBigCost(rng: () => number): bigint {
  const kind = rng();
  if (kind < 0.5) return BigInt(Math.floor(rng() * 21) - 10); // small, often negative
  if (kind < 0.8) return BigInt(Math.floor(rng() * 1e9)); // large positive
  // beyond Number.MAX_SAFE_INTEGER, sometimes negative
  const mag = (1n << 53n) + BigInt(Math.floor(rng() * 1e6)) * (1n << 40n);
  return rng() < 0.5 ? mag : -mag;
}

function buildRandom(
  rng: () => number,
  n: number,
  disableProb: number,
): AssignmentSolver {
  const s = new AssignmentSolver(n);
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      s.setCost(i, j, randomBigCost(rng));
      if (rng() < disableProb) s.disableEdge(i, j);
    }
  }
  return s;
}

function expectMatchesBruteForce(s: AssignmentSolver): AssignmentResult {
  const result = s.solve();
  const bf = bruteForce(s.n, (i, j) => s.isAllowed(i, j), (i, j) => s.getCost(i, j));
  expect(verifyCertificate(s, result)).toBe(true);
  if (bf === null) {
    expect(result.status).toBe('infeasible');
  } else {
    expect(result.status).toBe('optimal');
    if (result.status === 'optimal') expect(result.cost).toBe(bf.cost);
  }
  return result;
}

describe('random instances vs brute force', () => {
  for (const n of [1, 2, 3, 4, 5, 6]) {
    it(`n=${n} random costs and disabled edges`, () => {
      const rng = mulberry32(1000 + n);
      for (let t = 0; t < 150; t++) {
        const s = buildRandom(rng, n, t % 3 === 0 ? 0.25 : 0.05);
        expectMatchesBruteForce(s);
      }
    });
  }

  it('heavily disabled instances (many infeasible)', () => {
    const rng = mulberry32(77);
    let infeasibleCount = 0;
    for (let t = 0; t < 300; t++) {
      const s = buildRandom(rng, 4, 0.45);
      const r = expectMatchesBruteForce(s);
      if (r.status === 'infeasible') infeasibleCount++;
    }
    expect(infeasibleCount).toBeGreaterThan(0);
  });
});

describe('bigint costs', () => {
  it('handles costs beyond safe integer, mixed signs', () => {
    const s = new AssignmentSolver(2);
    const BIG = (1n << 62n) + 12345n;
    s.setCost(0, 0, BIG);
    s.setCost(0, 1, -BIG);
    s.setCost(1, 0, -BIG - 1n);
    s.setCost(1, 1, BIG + 7n);
    const r = s.solve();
    expect(r.status).toBe('optimal');
    if (r.status === 'optimal') {
      // options: BIG + (BIG+7) vs (-BIG) + (-BIG-1)
      expect(r.cost).toBe(-2n * BIG - 1n);
      expect(r.matching).toEqual([1, 0]);
    }
    expect(verifyCertificate(s, r)).toBe(true);
  });

  it('accepts number costs and converts to bigint', () => {
    const s = new AssignmentSolver(1);
    s.setCost(0, 0, -42);
    const r = s.solve();
    expect(r.status).toBe('optimal');
    if (r.status === 'optimal') expect(r.cost).toBe(-42n);
  });
});

describe('unique feasible edge disabled', () => {
  it('unique perfect matching exists, then its edge is disabled', () => {
    // Row 0 can only use col 0; row 1 can use col 0 or 1.
    // Unique perfect matching: (0,0),(1,1).
    const s = new AssignmentSolver(2);
    s.setCost(0, 0, 5n);
    s.setCost(0, 1, 1n);
    s.setCost(1, 0, 1n);
    s.setCost(1, 1, 9n);
    s.disableEdge(0, 1);
    let r = s.solve();
    expect(r.status).toBe('optimal');
    if (r.status === 'optimal') {
      expect(r.matching).toEqual([0, 1]);
      expect(r.cost).toBe(14n);
    }
    expect(verifyCertificate(s, r)).toBe(true);

    // Disable the unique feasible edge of row 0 -> infeasible.
    s.disableEdge(0, 0);
    r = s.solve();
    expect(r.status).toBe('infeasible');
    if (r.status === 'infeasible') {
      expect(r.hallDefect.rows).toEqual([0]);
      expect(r.hallDefect.neighborhood).toEqual([]);
      expect(r.hallDefect.deficiency).toBe(1);
    }
    expect(verifyCertificate(s, r)).toBe(true);
  });

  it('larger unique-matching instance becomes infeasible after one ban', () => {
    // Triangular allowed graph: unique perfect matching is the diagonal.
    const n = 5;
    const s = new AssignmentSolver(n);
    for (let i = 0; i < n; i++)
      for (let j = 0; j < n; j++) {
        s.setCost(i, j, BigInt(i * n + j));
        if (j > i) s.disableEdge(i, j);
      }
    let r = s.solve();
    expect(r.status).toBe('optimal');
    if (r.status === 'optimal') expect(r.matching).toEqual([0, 1, 2, 3, 4]);

    s.disableEdge(3, 3); // row 3 keeps cols 0..2, but Hall fails globally
    r = s.solve();
    expect(r.status).toBe('infeasible');
    expect(verifyCertificate(s, r)).toBe(true);
    const bf = bruteForce(n, (i, j) => s.isAllowed(i, j), (i, j) => s.getCost(i, j));
    expect(bf).toBeNull();
  });
});

describe('multiple optimal ties', () => {
  it('all-equal costs: any perfect matching is optimal', () => {
    const n = 4;
    const s = new AssignmentSolver(n);
    for (let i = 0; i < n; i++) for (let j = 0; j < n; j++) s.setCost(i, j, 3n);
    const r = s.solve();
    expect(r.status).toBe('optimal');
    if (r.status === 'optimal') expect(r.cost).toBe(12n);
    expect(verifyCertificate(s, r)).toBe(true);
  });

  it('structured ties match brute force and certify', () => {
    // Two distinct optimal matchings with equal cost.
    const s = new AssignmentSolver(3);
    const c = [
      [1n, 2n, 100n],
      [1n, 2n, 100n],
      [100n, 100n, 5n],
    ];
    for (let i = 0; i < 3; i++) for (let j = 0; j < 3; j++) s.setCost(i, j, c[i][j]);
    const r = s.solve();
    expect(r.status).toBe('optimal');
    if (r.status === 'optimal') {
      expect(r.cost).toBe(8n); // (1+2+5) via either diagonal swap
      expect([[0, 1, 2], [1, 0, 2]]).toContainEqual(r.matching);
    }
    expect(verifyCertificate(s, r)).toBe(true);
    const bf = bruteForce(3, (i, j) => s.isAllowed(i, j), (i, j) => s.getCost(i, j));
    expect(bf).not.toBeNull();
    expect((bf as { cost: bigint }).cost).toBe(8n);
  });

  it('random tie-heavy instances agree with brute force', () => {
    const rng = mulberry32(4242);
    for (let t = 0; t < 200; t++) {
      const n = 4;
      const s = new AssignmentSolver(n);
      for (let i = 0; i < n; i++)
        for (let j = 0; j < n; j++) s.setCost(i, j, BigInt(Math.floor(rng() * 3) - 1));
      expectMatchesBruteForce(s);
    }
  });
});

describe('local updates invalidate stale potentials', () => {
  it('cost update changes the optimum and the certificate stays valid', () => {
    const s = new AssignmentSolver(2);
    s.setCost(0, 0, 1n);
    s.setCost(0, 1, 100n);
    s.setCost(1, 0, 100n);
    s.setCost(1, 1, 1n);
    let r = s.solve();
    expect(r.status === 'optimal' && r.cost).toBe(2n);

    // Flip preference: now the anti-diagonal is cheap.
    s.setCost(0, 1, -50n);
    s.setCost(1, 0, -60n);
    r = s.solve();
    expect(r.status === 'optimal' && r.cost).toBe(-110n);
    expect(verifyCertificate(s, r)).toBe(true);
    if (r.status === 'optimal') expect(r.matching).toEqual([1, 0]);
  });

  it('random update sequences always match brute force', () => {
    const rng = mulberry32(999);
    for (let t = 0; t < 120; t++) {
      const n = 4;
      const s = buildRandom(rng, n, 0.15);
      expectMatchesBruteForce(s);
      const steps = 1 + Math.floor(rng() * 5);
      for (let k = 0; k < steps; k++) {
        const i = Math.floor(rng() * n);
        const j = Math.floor(rng() * n);
        const op = rng();
        if (op < 0.5) s.setCost(i, j, randomBigCost(rng));
        else if (op < 0.75) s.disableEdge(i, j);
        else s.enableEdge(i, j);
        expectMatchesBruteForce(s);
      }
    }
  });

  it('disabling the matched edge forces a different optimum', () => {
    const s = new AssignmentSolver(3);
    const c = [
      [0n, 5n, 5n],
      [5n, 0n, 5n],
      [5n, 5n, 0n],
    ];
    for (let i = 0; i < 3; i++) for (let j = 0; j < 3; j++) s.setCost(i, j, c[i][j]);
    let r = s.solve();
    expect(r.status === 'optimal' && r.cost).toBe(0n);

    // Ban every zero edge: optimum must jump to 15 with a fresh certificate.
    for (let i = 0; i < 3; i++) s.disableEdge(i, i);
    r = s.solve();
    expect(r.status === 'optimal' && r.cost).toBe(15n);
    expect(verifyCertificate(s, r)).toBe(true);
    if (r.status === 'optimal') {
      for (let i = 0; i < 3; i++) expect(r.matching[i]).not.toBe(i);
    }
  });

  it('solve() result is cached only until the next mutation', () => {
    const s = new AssignmentSolver(1);
    s.setCost(0, 0, 7n);
    const a = s.solve();
    expect(s.solve()).toBe(a); // cached identity
    s.setCost(0, 0, 8n);
    const b = s.solve();
    expect(b).not.toBe(a);
    expect(b.status === 'optimal' && b.cost).toBe(8n);
  });
});

describe('certificate verifier rejects tampering', () => {
  function solved(): { s: AssignmentSolver; r: AssignmentResult } {
    const s = new AssignmentSolver(3);
    const c = [
      [4n, 1n, 3n],
      [2n, 0n, 5n],
      [3n, 2n, 2n],
    ];
    for (let i = 0; i < 3; i++) for (let j = 0; j < 3; j++) s.setCost(i, j, c[i][j]);
    const r = s.solve();
    expect(r.status).toBe('optimal');
    return { s, r };
  }

  it('accepts the genuine certificate', () => {
    const { s, r } = solved();
    expect(verifyCertificate(s, r)).toBe(true);
  });

  it('rejects a perturbed dual', () => {
    const { s, r } = solved();
    if (r.status !== 'optimal') throw new Error('unreachable');
    const bad = {
      ...r,
      dual: { u: [r.dual.u[0] + 1n, r.dual.u[1], r.dual.u[2]], v: [...r.dual.v] },
    };
    expect(verifyCertificate(s, bad)).toBe(false);
  });

  it('rejects a wrong matching and a wrong cost', () => {
    const { s, r } = solved();
    if (r.status !== 'optimal') throw new Error('unreachable');
    expect(verifyCertificate(s, { ...r, matching: [2, 1, 0] })).toBe(false);
    expect(verifyCertificate(s, { ...r, cost: r.cost + 1n })).toBe(false);
    expect(verifyCertificate(s, { ...r, matching: [0, 0, 1] })).toBe(false);
  });

  it('rejects a bogus Hall defect', () => {
    const s = new AssignmentSolver(2);
    s.disableEdge(0, 0);
    s.disableEdge(0, 1);
    const r = s.solve();
    expect(r.status).toBe('infeasible');
    expect(verifyCertificate(s, r)).toBe(true);
    if (r.status !== 'infeasible') throw new Error('unreachable');
    // Row 1 has neighbors, so {1} is not a valid defect.
    expect(
      verifyCertificate(s, {
        status: 'infeasible',
        hallDefect: { rows: [1], neighborhood: [], deficiency: 1 },
      }),
    ).toBe(false);
    // Neighborhood missing an adjacent column.
    expect(
      verifyCertificate(s, {
        status: 'infeasible',
        hallDefect: { rows: [0, 1], neighborhood: [0], deficiency: 1 },
      }),
    ).toBe(false);
  });
});

describe('input validation', () => {
  it('rejects bad sizes and indices', () => {
    expect(() => new AssignmentSolver(0)).toThrow(RangeError);
    const s = new AssignmentSolver(2);
    expect(() => s.setCost(2, 0, 1n)).toThrow(RangeError);
    expect(() => s.disableEdge(0, -1)).toThrow(RangeError);
  });
});
