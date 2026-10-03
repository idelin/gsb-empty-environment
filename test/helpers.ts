import { WatershedEngine } from '../src/engine.ts';
import { segmentReference } from '../src/reference.ts';
import assert from 'node:assert/strict';

/** Deterministic PRNG so "random" tests are reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Shuffler factory for randomized tile/pixel processing order. */
export function shuffler(rng: () => number): (arr: number[]) => void {
  return (arr) => {
    for (let i = arr.length - 1; i > 0; i--) {
      const j = (rng() * (i + 1)) | 0;
      [arr[i], arr[j]] = [arr[j], arr[i]];
    }
  };
}

/** The engine's committed map must always equal the full-flood reference. */
export function assertMatchesReference(engine: WatershedEngine, context: string): void {
  assert.equal(engine.paused, false, `${context}: engine still paused`);
  const ref = segmentReference(engine.width, engine.height, engine.getHeights(), engine.getSeeds());
  const got = engine.getLabels();
  assert.deepEqual([...got], [...ref.labels], `${context}: engine != reference`);
}

