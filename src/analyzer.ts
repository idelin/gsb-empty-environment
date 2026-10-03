import type { HashFn } from "./hashes.ts";
import { encodeKey } from "./hashes.ts";
import { ConsistentHashRing } from "./ring.ts";
import {
  chiSquarePValue,
  chiSquareUniform,
  mean,
  minMax,
  stddev,
} from "./stats.ts";

export type Verdict = "PASS" | "WARN" | "FAIL";

export interface LoadReport {
  hashName: string;
  nodeCount: number;
  sampleCount: number;
  vnodesPerNode: number;
  ringPoints: number;
  ringCollisions: number;
  loads: Uint32Array;
  mean: number;
  stddev: number;
  cv: number;
  /** Theoretical CV of an ideal ring: 1/sqrt(vnodesPerNode). */
  cvTheory: number;
  /** cv / cvTheory; ~1.0 means the hash reaches ideal ring balance. */
  cvRatio: number;
  min: number;
  max: number;
  maxMinRatio: number;
  chi2: number;
  df: number;
  pValue: number;
  verdict: Verdict;
}

export const CV_RATIO_WARN = 1.5;
export const CV_RATIO_FAIL = 2.5;

/**
 * Ring-load verdict. A chi-square test against perfect uniformity is the
 * wrong null model for a consistent-hash ring: even an ideal hash shows
 * load CV ~= 1/sqrt(vnodes). We therefore grade the measured CV against
 * that theoretical floor instead.
 */
export function verdictForCv(cv: number, cvTheory: number): Verdict {
  const ratio = cvTheory === 0 ? Infinity : cv / cvTheory;
  if (ratio > CV_RATIO_FAIL) return "FAIL";
  if (ratio > CV_RATIO_WARN) return "WARN";
  return "PASS";
}

/**
 * Map `sampleCount` keys onto the ring and measure per-node load balance:
 * stddev, max/min ratio and a chi-square statistic across nodes.
 * Keys are produced by `nextKey(i)` so callers control the key stream.
 */
export function analyzeLoad(options: {
  hashName: string;
  hash: HashFn;
  nodes: string[];
  sampleCount: number;
  vnodesPerNode: number;
  nextKey: (i: number) => string;
  /** Pre-computed hashes; when present, keys are not re-hashed. */
  hashes?: Uint32Array;
}): LoadReport {
  const { hashName, hash, nodes, sampleCount, vnodesPerNode, nextKey } = options;
  const ring = new ConsistentHashRing(nodes, vnodesPerNode, hash);
  const loads = new Uint32Array(nodes.length);
  const hashes = options.hashes;
  for (let i = 0; i < sampleCount; i++) {
    const h = hashes ? hashes[i] : hash(encodeKey(nextKey(i)));
    loads[ring.lookup(h)]++;
  }
  const mu = mean(loads);
  const sd = stddev(loads, mu);
  const { min, max } = minMax(loads);
  const { stat, df } = chiSquareUniform(loads);
  const pValue = chiSquarePValue(stat, df);
  const cv = mu === 0 ? 0 : sd / mu;
  const cvTheory = 1 / Math.sqrt(vnodesPerNode);
  return {
    hashName,
    nodeCount: nodes.length,
    sampleCount,
    vnodesPerNode,
    ringPoints: ring.positions.length,
    ringCollisions: ring.collisions,
    loads,
    mean: mu,
    stddev: sd,
    cv,
    cvTheory,
    cvRatio: cvTheory === 0 ? Infinity : cv / cvTheory,
    min,
    max,
    maxMinRatio: min === 0 ? Infinity : max / min,
    chi2: stat,
    df,
    pValue,
    verdict: verdictForCv(cv, cvTheory),
  };
}

export type WidthAnomaly = "none" | "non-uniform" | "over-regular";

export interface BitWidthPoint {
  bits: number;
  buckets: number;
  chi2: number;
  df: number;
  /** chi2 / df; ~1.0 means indistinguishable from uniform. */
  normalized: number;
  pValue: number;
  anomaly: WidthAnomaly;
  deficient: boolean;
}

export const DEFICIENCY_P_THRESHOLD = 1e-3;
export const OVER_REGULAR_P_THRESHOLD = 1 - 1e-4;

function classify(pValue: number): WidthAnomaly {
  if (pValue < DEFICIENCY_P_THRESHOLD) return "non-uniform";
  if (pValue > OVER_REGULAR_P_THRESHOLD) return "over-regular";
  return "none";
}

/**
 * Variance curve over reduced bit widths: truncate each 32-bit hash to its
 * low `b` bits, bucket the samples into 2^b bins and run a chi-square
 * uniformity test per width. A hash with proper avalanche stays flat near
 * normalized == 1 at every width; weak diffusion shows up as a spike
 * (non-uniform) or as a suspiciously perfect fit (over-regular).
 */
export function analyzeBitWidths(
  hashes: Uint32Array,
  widths: number[],
): BitWidthPoint[] {
  const points: BitWidthPoint[] = [];
  for (const bits of widths) {
    if (bits < 1 || bits > 24) throw new RangeError(`bad width ${bits}`);
    const buckets = 1 << bits;
    const counts = new Uint32Array(buckets);
    const mask = buckets - 1;
    for (let i = 0; i < hashes.length; i++) counts[hashes[i] & mask]++;
    const { stat, df } = chiSquareUniform(counts);
    const pValue = chiSquarePValue(stat, df);
    const anomaly = classify(pValue);
    points.push({
      bits,
      buckets,
      chi2: stat,
      df,
      normalized: stat / df,
      pValue,
      anomaly,
      deficient: anomaly !== "none",
    });
  }
  return points;
}

/** Hash every key once so ring + bit-width passes reuse the values. */
export function hashSamples(
  hash: HashFn,
  sampleCount: number,
  nextKey: (i: number) => string,
): Uint32Array {
  const out = new Uint32Array(sampleCount);
  for (let i = 0; i < sampleCount; i++) out[i] = hash(encodeKey(nextKey(i)));
  return out;
}
