/**
 * Exact factorization of polynomials over Q:
 *  - Yun's square-free factorization (via gcd with the derivative)
 *  - Kronecker's algorithm for irreducible factors (interpolation over
 *    divisor tuples of integer values)
 * Degrees here are small (minimal polynomials of radical expressions),
 * so Kronecker's exponential worst case is not a concern.
 */
import { Rational, gcdBig } from './rational.ts';
import { Poly, toIntegerCoeffs } from './poly.ts';

function evalInt(coeffs: bigint[], x: bigint): bigint {
  let acc = 0n;
  for (let i = coeffs.length - 1; i >= 0; i--) acc = acc * x + coeffs[i];
  return acc;
}

/** all divisors of n (n != 0), including negatives, small absolute values first */
function divisorsWithSign(n: bigint): bigint[] {
  const a = n < 0n ? -n : n;
  const pos: bigint[] = [];
  for (let d = 1n; d * d <= a; d++) {
    if (a % d === 0n) {
      pos.push(d);
      if (d * d !== a) pos.push(a / d);
    }
  }
  pos.sort((u, v) => (u < v ? -1 : u > v ? 1 : 0));
  const out: bigint[] = [];
  for (const d of pos) out.push(d, -d);
  return out;
}

/** Lagrange interpolation through integer points, exact rational arithmetic */
function interpolate(xs: bigint[], ys: bigint[]): Poly {
  let result = Poly.zero();
  for (let i = 0; i < xs.length; i++) {
    let term = Poly.constant(Rational.make(ys[i], 1n));
    for (let j = 0; j < xs.length; j++) {
      if (j === i) continue;
      term = term.mul(Poly.of([Rational.make(-xs[j], 1n), Rational.ONE]));
      term = term.scale(Rational.make(1n, xs[i] - xs[j]));
    }
    result = result.add(term);
  }
  return result;
}

/** clear denominators, divide out the content, make leading coefficient positive */
function primitivePart(h: Poly): Poly {
  const ints = toIntegerCoeffs(h);
  let g = 0n;
  for (const c of ints) g = gcdBig(g, c);
  if (g === 0n) g = 1n;
  const sign = ints[ints.length - 1] < 0n ? -1n : 1n;
  return Poly.of(ints.map((c) => Rational.make((sign * c) / g, 1n)));
}

function* cartesian(lists: bigint[][]): Generator<bigint[]> {
  if (lists.length === 0) {
    yield [];
    return;
  }
  const [head, ...tail] = lists;
  for (const v of head) {
    for (const rest of cartesian(tail)) yield [v, ...rest];
  }
}

const MAX_CANDIDATES = 4_000_000;

/**
 * Kronecker's algorithm: look for a factor of exact degree d of the
 * integer-valued polynomial f. Returns a primitive factor or null.
 */
function kronecker(f: Poly, d: number): Poly | null {
  const ints = toIntegerCoeffs(f);
  const xs: bigint[] = [];
  const cs: bigint[] = [];
  let i = 0n;
  while (xs.length < d + 1) {
    const candidates = i === 0n ? [0n] : [i, -i];
    for (const a of candidates) {
      const c = evalInt(ints, a);
      if (c === 0n) {
        // f(a) = 0, so (x - a) divides f
        return Poly.of([Rational.make(-a, 1n), Rational.ONE]);
      }
      xs.push(a);
      cs.push(c);
      if (xs.length > d) break;
    }
    i++;
  }
  const divLists = cs.map(divisorsWithSign);
  let total = 1;
  for (const l of divLists) total *= l.length;
  if (total > MAX_CANDIDATES) throw new Error('kronecker: candidate explosion');
  for (const tuple of cartesian(divLists)) {
    const h = interpolate(xs, tuple);
    if (h.deg < 1) continue;
    const hp = primitivePart(h);
    if (f.mod(hp).isZero()) return hp;
  }
  return null;
}

/** find some nontrivial factor of f (smallest degree first), or null if irreducible */
function findFactor(f: Poly): Poly | null {
  for (let d = 1; 2 * d <= f.deg; d++) {
    const g = kronecker(f, d);
    if (g !== null) return g;
  }
  return null;
}

/**
 * Factor a square-free polynomial over Q into monic irreducible factors.
 */
export function factorSquareFree(f: Poly): Poly[] {
  if (f.deg <= 0) return [];
  if (f.deg === 1) return [f.monic()];
  const g = findFactor(f);
  if (g === null) return [f.monic()];
  return [...factorSquareFree(g), ...factorSquareFree(f.div(g))];
}

/**
 * Full factorization over Q (Yun's square-free factorization + Kronecker).
 * Returns monic irreducible factors with multiplicities.
 */
export function factor(f: Poly): Array<{ factor: Poly; multiplicity: number }> {
  const out: Array<{ factor: Poly; multiplicity: number }> = [];
  if (f.deg <= 0) return out;
  let c = f.gcd(f.derivative());
  let w = f.div(c);
  let i = 1;
  while (!w.isOne()) {
    const y = w.gcd(c);
    const z = w.div(y);
    if (z.deg >= 1) {
      for (const g of factorSquareFree(z)) out.push({ factor: g, multiplicity: i });
    }
    w = y;
    c = c.div(y);
    i++;
  }
  return out;
}
