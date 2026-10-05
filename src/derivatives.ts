/**
 * Derivative parameters at the nodes.
 *
 * The rules are FIXED (no user tuning):
 *  - interior nodes: Fritsch-Carlson weighted harmonic mean of the
 *    adjacent secant slopes, zero whenever the secants change sign or
 *    either secant is zero;
 *  - endpoints: the standard one-sided three-point estimate with the
 *    Fritsch-Carlson monotonicity clamp.
 *
 * These rules guarantee sign(d_i) agrees with the local trend of the
 * data, which is exactly what the segment certificates rely on.
 */

/** One-sided endpoint derivative (fixed rule), with monotonicity clamp. */
function endpointDerivative(h0: number, h1: number, delta0: number, delta1: number): number {
  let d = ((2 * h0 + h1) * delta0 - h0 * delta1) / (h0 + h1);
  if (Math.sign(d) !== Math.sign(delta0)) {
    d = 0;
  } else if (Math.sign(delta0) !== Math.sign(delta1) && Math.abs(d) > 3 * Math.abs(delta0)) {
    d = 3 * delta0;
  }
  return d;
}

/**
 * Fritsch-Carlson / PCHIP derivatives for nodes xs and ordinates ys
 * (xs strictly increasing, both arrays length n >= 1).
 */
export function pchipDerivatives(xs: readonly number[], ys: readonly number[]): number[] {
  const n = xs.length;
  if (n === 1) {
    return [0];
  }
  const h: number[] = [];
  const delta: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    h.push(xs[i + 1] - xs[i]);
    delta.push((ys[i + 1] - ys[i]) / h[i]);
  }
  if (n === 2) {
    return [delta[0], delta[0]];
  }

  const d = new Array<number>(n);
  d[0] = endpointDerivative(h[0], h[1], delta[0], delta[1]);
  d[n - 1] = endpointDerivative(h[n - 2], h[n - 3], delta[n - 2], delta[n - 3]);

  for (let i = 1; i < n - 1; i++) {
    if (delta[i - 1] === 0 || delta[i] === 0 || Math.sign(delta[i - 1]) !== Math.sign(delta[i])) {
      d[i] = 0;
    } else {
      const w1 = 2 * h[i] + h[i - 1];
      const w2 = h[i] + 2 * h[i - 1];
      d[i] = (w1 + w2) / (w1 / delta[i - 1] + w2 / delta[i]);
    }
  }
  return d;
}
