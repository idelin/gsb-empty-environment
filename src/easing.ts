/** Easing functions map linear progress p in [0, 1] to eased progress. */
export type Easing = (p: number) => number;

export const linear: Easing = (p) => p;

export const easeInCubic: Easing = (p) => p * p * p;

export const easeOutCubic: Easing = (p) => 1 - Math.pow(1 - p, 3);

export const easeInOutCubic: Easing = (p) =>
  p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2;

/** CSS-style steps(). pos: 'end' (jump-end) or 'start' (jump-start). */
export function steps(count: number, pos: 'end' | 'start' = 'end'): Easing {
  if (!Number.isInteger(count) || count < 1) {
    throw new Error(`steps count must be a positive integer, got ${count}`);
  }
  if (pos === 'end') {
    return (p) => (p >= 1 ? 1 : Math.max(0, Math.floor(p * count) / count));
  }
  return (p) =>
    p <= 0 ? 0 : Math.min(1, (Math.floor(p * count) + 1) / count);
}

/** CSS-style cubic-bezier(x1, y1, x2, y2) solved via Newton–Raphson + bisection. */
export function cubicBezier(
  x1: number,
  y1: number,
  x2: number,
  y2: number,
): Easing {
  const ax = 3 * x1 - 3 * x2 + 1;
  const bx = 3 * x2 - 6 * x1;
  const cx = 3 * x1;
  const ay = 3 * y1 - 3 * y2 + 1;
  const by = 3 * y2 - 6 * y1;
  const cy = 3 * y1;
  const sampleX = (t: number) => ((ax * t + bx) * t + cx) * t;
  const sampleY = (t: number) => ((ay * t + by) * t + cy) * t;
  const sampleDX = (t: number) => (3 * ax * t + 2 * bx) * t + cx;
  return (p) => {
    if (p <= 0) return 0;
    if (p >= 1) return 1;
    let t = p;
    for (let i = 0; i < 8; i++) {
      const err = sampleX(t) - p;
      if (Math.abs(err) < 1e-9) return sampleY(t);
      const d = sampleDX(t);
      if (Math.abs(d) < 1e-9) break;
      t -= err / d;
    }
    let lo = 0;
    let hi = 1;
    t = p;
    while (hi - lo > 1e-9) {
      const x = sampleX(t);
      if (Math.abs(x - p) < 1e-9) break;
      if (x < p) lo = t;
      else hi = t;
      t = (lo + hi) / 2;
    }
    return sampleY(t);
  };
}
