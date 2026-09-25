import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { Fraction } from '../../src/rational.ts';
import type { SceneFixture } from '../../src/tree.ts';
import type { ScrollTransaction } from '../../src/engine.ts';

export function loadScene(): SceneFixture {
  const path = fileURLToPath(new URL('../../fixtures/scene.json', import.meta.url));
  return JSON.parse(readFileSync(path, 'utf8')) as SceneFixture;
}

/** Deterministic PRNG so randomized reference checks are reproducible. */
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Independent exact-arithmetic model of the engine's consume step. Mirrors
 * src/engine.ts consumeAt but with Fractions, so tests can verify that the
 * float engine loses nothing: for dyadic inputs the two must agree exactly.
 */
export class RefNode {
  offset = Fraction.of(0);
  residual = Fraction.of(0);
  readonly max: Fraction;
  readonly snapStep: Fraction | null;
  constructor(max: Fraction, snapStep: Fraction | null) {
    this.max = max;
    this.snapStep = snapStep;
  }
}

export function refConsume(node: RefNode, amount: Fraction): { consumed: Fraction; passed: Fraction } {
  const incoming = amount.add(node.residual);
  const dir = incoming.sign();
  const room = dir > 0 ? node.max.sub(node.offset) : dir < 0 ? node.offset : Fraction.of(0);
  const applied = incoming.abs().cmp(room) <= 0 ? incoming : room.mul(Fraction.of(dir));
  let newOffset = node.offset.add(applied);
  if (node.snapStep !== null) newOffset = newOffset.snapTo(node.snapStep);
  if (newOffset.sign() < 0) newOffset = Fraction.of(0);
  if (newOffset.cmp(node.max) > 0) newOffset = node.max;
  const actual = newOffset.sub(node.offset);
  node.offset = newOffset;
  node.residual = applied.sub(actual);
  return { consumed: actual, passed: incoming.sub(applied) };
}

export interface AxisTotals {
  consumed: number;
  residualBefore: number;
  residualAfter: number;
  passed: number;
}

/** Per-layer conservation identity: received + residualBefore === consumed + passed + residualAfter. */
export function layerConservationError(transaction: ScrollTransaction): number {
  let worst = 0;
  for (const layer of transaction.layers) {
    for (const axis of [layer.x, layer.y]) {
      const lhs = axis.received + axis.residualBefore;
      const rhs = axis.consumed + axis.passed + axis.residualAfter;
      worst = Math.max(worst, Math.abs(lhs - rhs));
    }
  }
  return worst;
}

/**
 * Whole-transaction identity: the input delta, tracked through every layer
 * (including axis transforms), ends up as consumed + residualDelta + discarded.
 */
export function transactionConservationError(transaction: ScrollTransaction, input: { x: number; y: number }): number {
  const cursor = { x: input.x, y: input.y };
  for (const layer of transaction.layers) {
    if (layer.transformApplied === 'swap') {
      const tmp = cursor.x;
      cursor.x = cursor.y;
      cursor.y = tmp;
    }
    cursor.x -= layer.x.consumed + (layer.x.residualAfter - layer.x.residualBefore);
    cursor.y -= layer.y.consumed + (layer.y.residualAfter - layer.y.residualBefore);
  }
  return Math.max(Math.abs(cursor.x - transaction.discarded.x), Math.abs(cursor.y - transaction.discarded.y));
}
