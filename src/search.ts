import { rgba8Equals } from './color.ts';
import type { Rgba8 } from './color.ts';
import type { Layer, QuantizedResult } from './compose.ts';
import { composeGrouped, composeOneShot } from './compose.ts';

// A 3-layer stack of gray premultiplied 8-bit pixels, index 1 = bottom.
// RGB channels are independent under "over", so a minimal-max counterexample
// exists in full RGBA iff one exists in gray (keep the differing channel,
// zero the others); searching gray stacks is therefore without loss of
// generality for minimality in the max-channel-value metric.
export interface GrayStack {
  v1: number;
  a1: number;
  v2: number;
  a2: number;
  v3: number;
  a3: number;
}

export interface Counterexample {
  bound: number;
  stack: GrayStack;
  grouping: number[];
  oneShot: QuantizedResult;
  grouped: QuantizedResult;
  stacksBelowBound: number;
  stacksAtBound: number;
}

const MAX = 255;
const MAX_SQ = MAX * MAX;

const roundHalfUpInt = (num: number, den: number): number =>
  Math.floor((2 * num + den) / (2 * den));

// Straight byte for a gray channel: un-premultiply by the exact alpha, then
// round; zero alpha maps to transparent black (channel 0), as in color.ts.
const straightChannel = (premultNum: number, premultDen: number, alphaNum: number, alphaDen: number): number =>
  alphaNum === 0 ? 0 : roundHalfUpInt(premultNum * alphaDen * MAX, premultDen * alphaNum);

// Exact-integer mirror of composeOneShot vs composeGrouped on gray stacks.
// With includeStraight, compares everything the library outputs: premultiplied
// bytes, alpha bytes, and straight (un-premultiplied) bytes; otherwise only
// the premultiplied bytes. All numerators stay below 2^53, so Number
// arithmetic is exact.
export function differingGroupings(s: GrayStack, includeStraight = true): number[][] {
  const { v1, a1, v2, a2, v3, a3 } = s;
  const out: number[][] = [];

  // Grouping [1, 2]: top pair composited and quantized, then over the bottom.
  {
    const nc = MAX * v3 + v2 * (MAX - a3);
    const na = MAX * a3 + a2 * (MAX - a3);
    const qc = roundHalfUpInt(nc, MAX);
    const qa = roundHalfUpInt(na, MAX);
    const gColorNum = MAX * qc + v1 * (MAX - qa);
    const gAlphaNum = MAX * qa + a1 * (MAX - qa);
    const oColorNum = MAX * nc + v1 * (MAX_SQ - na);
    const oAlphaNum = MAX * na + a1 * (MAX_SQ - na);
    const differs =
      roundHalfUpInt(gColorNum, MAX) !== roundHalfUpInt(oColorNum, MAX_SQ) ||
      roundHalfUpInt(gAlphaNum, MAX) !== roundHalfUpInt(oAlphaNum, MAX_SQ) ||
      (includeStraight &&
        straightChannel(gColorNum, MAX, gAlphaNum, MAX) !==
          straightChannel(oColorNum, MAX_SQ, oAlphaNum, MAX_SQ));
    if (differs) out.push([1, 2]);
  }

  // Grouping [2, 1]: bottom pair composited and quantized, then under the top.
  {
    const mc = MAX * v2 + v1 * (MAX - a2);
    const ma = MAX * a2 + a1 * (MAX - a2);
    const pc = roundHalfUpInt(mc, MAX);
    const pa = roundHalfUpInt(ma, MAX);
    const gColorNum = MAX * v3 + pc * (MAX - a3);
    const gAlphaNum = MAX * a3 + pa * (MAX - a3);
    const oColorNum = MAX_SQ * v3 + mc * (MAX - a3);
    const oAlphaNum = MAX_SQ * a3 + ma * (MAX - a3);
    const differs =
      roundHalfUpInt(gColorNum, MAX) !== roundHalfUpInt(oColorNum, MAX_SQ) ||
      roundHalfUpInt(gAlphaNum, MAX) !== roundHalfUpInt(oAlphaNum, MAX_SQ) ||
      (includeStraight &&
        straightChannel(gColorNum, MAX, gAlphaNum, MAX) !==
          straightChannel(oColorNum, MAX_SQ, oAlphaNum, MAX_SQ));
    if (differs) out.push([2, 1]);
  }

  return out;
}

export function stackToLayers(s: GrayStack): Layer[] {
  const px = (v: number, a: number): Layer => ({
    format: 'premultiplied',
    color: { r: v, g: v, b: v, a } as Rgba8,
  });
  return [px(s.v1, s.a1), px(s.v2, s.a2), px(s.v3, s.a3)];
}

const keyOf = (s: GrayStack): number[] => [s.v1, s.a1, s.v2, s.a2, s.v3, s.a3];

const sumOf = (s: GrayStack): number => keyOf(s).reduce((x, y) => x + y, 0);

const lexCompare = (x: number[], y: number[]): number => {
  for (let i = 0; i < x.length; i += 1) {
    if (x[i] !== y[i]) return x[i] - y[i];
  }
  return 0;
};

// Exhaustive search ordered by the max byte value in the stack, then by the
// sum of all six bytes, then lexicographically. Two layers can never differ
// (grouping [2] is one-shot; [1,1] quantizes 8-bit inputs to themselves), so
// three layers is the minimal depth and only [1,2] / [2,1] are non-trivial.
export function findMinimalCounterexample(maxBound = MAX, includeStraight = true): Counterexample | null {
  let stacksBelowBound = 0;
  for (let bound = 0; bound <= maxBound; bound += 1) {
    let stacksAtBound = 0;
    const hits: { stack: GrayStack; groupings: number[][] }[] = [];
    for (let a1 = 0; a1 <= bound; a1 += 1) {
      for (let v1 = 0; v1 <= a1; v1 += 1) {
        for (let a2 = 0; a2 <= bound; a2 += 1) {
          for (let v2 = 0; v2 <= a2; v2 += 1) {
            for (let a3 = 0; a3 <= bound; a3 += 1) {
              for (let v3 = 0; v3 <= a3; v3 += 1) {
                if (Math.max(v1, a1, v2, a2, v3, a3) !== bound) continue;
                stacksAtBound += 1;
                const stack: GrayStack = { v1, a1, v2, a2, v3, a3 };
                const groupings = differingGroupings(stack, includeStraight);
                if (groupings.length > 0) hits.push({ stack, groupings });
              }
            }
          }
        }
      }
    }
    if (hits.length > 0) {
      hits.sort((x, y) => sumOf(x.stack) - sumOf(y.stack) || lexCompare(keyOf(x.stack), keyOf(y.stack)));
      const best = hits[0];
      const layers = stackToLayers(best.stack);
      const grouping = best.groupings[0];
      const oneShot = composeOneShot(layers);
      const grouped = composeGrouped(layers, grouping);
      const differs = includeStraight
        ? !rgba8Equals(oneShot.premultiplied, grouped.premultiplied) ||
          !rgba8Equals(oneShot.straight, grouped.straight)
        : !rgba8Equals(oneShot.premultiplied, grouped.premultiplied);
      if (!differs) {
        throw new Error('Internal error: integer predicate disagrees with exact pipeline');
      }
      return {
        bound,
        stack: best.stack,
        grouping,
        oneShot,
        grouped,
        stacksBelowBound,
        stacksAtBound,
      };
    }
    stacksBelowBound += stacksAtBound;
  }
  return null;
}
