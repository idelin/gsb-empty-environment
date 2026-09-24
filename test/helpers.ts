import { digestText } from "../src/digest.ts";
import type { Op, Patch } from "../src/ops.ts";
import { PatchApplyError } from "../src/ops.ts";
import { utf16Length } from "../src/utf16.ts";

/**
 * Stepwise reference implementation: applies a patch one op at a time using
 * plain string slicing. Used as an independent model to cross-check the
 * optimized applier, composition and inversion.
 */
export function referenceApply(base: string, patch: Patch): string {
  if (utf16Length(base) !== patch.baseLength || digestText(base) !== patch.baseDigest) {
    throw new PatchApplyError("reference: base digest mismatch");
  }
  let rest = base;
  let out = "";
  for (const op of patch.ops) {
    switch (op.kind) {
      case "retain":
        out += rest.slice(0, op.count);
        rest = rest.slice(op.count);
        break;
      case "delete":
        rest = rest.slice(op.count);
        break;
      case "insert":
        out += op.text;
        break;
    }
  }
  if (rest.length !== 0) throw new PatchApplyError("reference: trailing input");
  return out;
}

/** Stepwise application of a whole chain of patches. */
export function referenceApplyChain(base: string, patches: Patch[]): string {
  let text = base;
  for (const p of patches) text = referenceApply(text, p);
  return text;
}

/** mulberry32: tiny deterministic PRNG for reproducible fuzz cases. */
export function rng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ALPHABET = ["a", "b", "c", "foo", "foo", "\n", "\r\n", "😀", "é", " ", "x"];

export function randomText(rand: () => number, maxTokens = 24): string {
  const n = Math.floor(rand() * maxTokens);
  let s = "";
  for (let i = 0; i < n; i++) s += ALPHABET[Math.floor(rand() * ALPHABET.length)];
  return s;
}

export interface Splice {
  start: number;
  deleteCount: number;
  text: string;
}

/** A random splice whose cuts never split surrogate pairs of `text`. */
export function randomSplice(rand: () => number, text: string): Splice {
  const safeOffsets: number[] = [];
  for (let i = 0; i <= text.length; i++) {
    const high = i > 0 && text.charCodeAt(i - 1) >= 0xd800 && text.charCodeAt(i - 1) <= 0xdbff;
    const low = i < text.length && text.charCodeAt(i) >= 0xdc00 && text.charCodeAt(i) <= 0xdfff;
    if (!(high && low)) safeOffsets.push(i);
  }
  const start = safeOffsets[Math.floor(rand() * safeOffsets.length)];
  const tail = safeOffsets.filter((o) => o >= start);
  const end = tail[Math.floor(rand() * tail.length)];
  return { start, deleteCount: end - start, text: randomText(rand, 8) };
}

export function applySplice(text: string, splice: Splice): string {
  return text.slice(0, splice.start) + splice.text + text.slice(splice.start + splice.deleteCount);
}

/** Build the patch ops of a single splice against `base` (ids supplied). */
export function spliceOps(base: string, splice: Splice, id: string): Op[] {
  const ops: Op[] = [];
  if (splice.start > 0) ops.push({ kind: "retain", count: splice.start });
  if (splice.deleteCount > 0) ops.push({ kind: "delete", count: splice.deleteCount });
  if (splice.text.length > 0) ops.push({ kind: "insert", id, text: splice.text });
  const tail = base.length - splice.start - splice.deleteCount;
  if (tail > 0) ops.push({ kind: "retain", count: tail });
  return ops;
}
