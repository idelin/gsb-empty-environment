export type Easing = (t: number) => number;

export const linear: Easing = (t) => t;
export const easeInQuad: Easing = (t) => t * t;
export const easeOutQuad: Easing = (t) => t * (2 - t);
export const easeInOutQuad: Easing = (t) =>
  t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
export const easeInCubic: Easing = (t) => t * t * t;
export const easeOutCubic: Easing = (t) => 1 - Math.pow(1 - t, 3);
export const easeInOutCubic: Easing = (t) =>
  t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;

const NEWTON_ITERATIONS = 4;
const NEWTON_MIN_SLOPE = 0.001;
const SUBDIVISION_PRECISION = 1e-7;
const SUBDIVISION_MAX_ITERATIONS = 10;
const SPLINE_TABLE_SIZE = 11;
const SAMPLE_STEP = 1 / (SPLINE_TABLE_SIZE - 1);

export function cubicBezier(x1: number, y1: number, x2: number, y2: number): Easing {
  if (!(0 <= x1 && x1 <= 1 && 0 <= x2 && x2 <= 1)) {
    throw new Error('cubic-bezier x values must be in [0, 1]');
  }
  if (x1 === y1 && x2 === y2) return linear;

  const A = (a1: number, a2: number) => 1 - 3 * a2 + 3 * a1;
  const B = (a1: number, a2: number) => 3 * a2 - 6 * a1;
  const C = (a1: number) => 3 * a1;
  const calc = (t: number, a1: number, a2: number) => ((A(a1, a2) * t + B(a1, a2)) * t + C(a1)) * t;
  const slope = (t: number, a1: number, a2: number) =>
    3 * A(a1, a2) * t * t + 2 * B(a1, a2) * t + C(a1);

  const samples = new Float64Array(SPLINE_TABLE_SIZE);
  for (let i = 0; i < SPLINE_TABLE_SIZE; i++) samples[i] = calc(i * SAMPLE_STEP, x1, x2);

  const newtonRaphson = (x: number, guess: number): number => {
    let t = guess;
    for (let i = 0; i < NEWTON_ITERATIONS; i++) {
      const s = slope(t, x1, x2);
      if (s === 0) return t;
      t -= (calc(t, x1, x2) - x) / s;
    }
    return t;
  };

  const bisect = (x: number, lo: number, hi: number): number => {
    let t = 0;
    let a = lo;
    let b = hi;
    for (let i = 0; i < SUBDIVISION_MAX_ITERATIONS; i++) {
      t = a + (b - a) / 2;
      const err = calc(t, x1, x2) - x;
      if (Math.abs(err) <= SUBDIVISION_PRECISION) break;
      if (err > 0) b = t;
      else a = t;
    }
    return t;
  };

  const tForX = (x: number): number => {
    let start = 0;
    let i = 1;
    for (; i !== SPLINE_TABLE_SIZE - 1 && samples[i] <= x; i++) start += SAMPLE_STEP;
    i--;
    const dist = (x - samples[i]) / (samples[i + 1] - samples[i]);
    const guess = start + dist * SAMPLE_STEP;
    const initialSlope = slope(guess, x1, x2);
    if (initialSlope >= NEWTON_MIN_SLOPE) return newtonRaphson(x, guess);
    if (initialSlope === 0) return guess;
    return bisect(x, start, start + SAMPLE_STEP);
  };

  return (x) => (x === 0 || x === 1 ? x : calc(tForX(x), y1, y2));
}

export function steps(count: number, position: 'start' | 'end' = 'end'): Easing {
  if (!Number.isInteger(count) || count <= 0) throw new Error('steps count must be a positive integer');
  return position === 'start'
    ? (t) => Math.min(Math.ceil(t * count) / count, 1)
    : (t) => Math.floor(t * count) / count;
}

// Time reversal of a curve: the reversed easing evaluates the original at
// mirrored time. This is exact time inversion, not a swap of endpoint values.
export function reverseEasing(easing: Easing): Easing {
  return (t) => 1 - easing(1 - t);
}

const NAMED_EASINGS = {
  linear,
  'ease-in': easeInQuad,
  'ease-out': easeOutQuad,
  'ease-in-out': easeInOutQuad,
  'ease-in-cubic': easeInCubic,
  'ease-out-cubic': easeOutCubic,
  'ease-in-out-cubic': easeInOutCubic,
} as const;

export type EasingName = keyof typeof NAMED_EASINGS;

export function resolveEasing(easing: EasingName | Easing | undefined): Easing {
  if (easing === undefined) return linear;
  if (typeof easing === 'function') return easing;
  const found = NAMED_EASINGS[easing];
  if (!found) throw new Error(`unknown easing: ${easing}`);
  return found;
}
