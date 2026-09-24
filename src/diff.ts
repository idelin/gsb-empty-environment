import { digestText } from "./digest.ts";
import { randomOpId, type IdGen } from "./ids.ts";
import { makePatch, type Op, type Patch } from "./ops.ts";
import { splitsSurrogatePair, utf16Length } from "./utf16.ts";

function commonPrefix(a: string, b: string): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a.charCodeAt(i) === b.charCodeAt(i)) i += 1;
  // Never leave the cut inside a surrogate pair of either string.
  while (splitsSurrogatePair(a, i) || splitsSurrogatePair(b, i)) i -= 1;
  return i;
}

function commonSuffix(a: string, b: string, maxLen: number): number {
  const n = Math.min(Math.min(a.length, b.length), maxLen);
  let i = 0;
  while (i < n && a.charCodeAt(a.length - 1 - i) === b.charCodeAt(b.length - 1 - i)) i += 1;
  // `i` code units are shared at the tail; the cut positions are
  // a.length - i and b.length - i. Keep them out of surrogate pairs.
  while (splitsSurrogatePair(a, a.length - i) || splitsSurrogatePair(b, b.length - i)) i -= 1;
  return i;
}

/**
 * Build a patch that turns `base` into `edited` (prefix/suffix trim diff).
 * Generated boundaries never split surrogate pairs; the inserted middle
 * carries a fresh operation identity.
 */
export function diffPatch(base: string, edited: string, idGen: IdGen = randomOpId): Patch {
  const pre = commonPrefix(base, edited);
  const suf = commonSuffix(base, edited, Math.min(base.length, edited.length) - pre);
  const deleted = base.slice(pre, base.length - suf);
  const inserted = edited.slice(pre, edited.length - suf);
  const ops: Op[] = [];
  if (pre > 0) ops.push({ kind: "retain", count: pre });
  if (deleted.length > 0) ops.push({ kind: "delete", count: deleted.length, text: deleted });
  if (inserted.length > 0) ops.push({ kind: "insert", id: idGen(), text: inserted });
  if (suf > 0) ops.push({ kind: "retain", count: suf });
  return makePatch(utf16Length(base), digestText(base), ops);
}
