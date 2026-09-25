/**
 * Paths are immutable cons-lists (structural sharing), so decoding a
 * 100k-deep chain costs O(depth) total instead of O(depth^2) copies.
 * Arrays of segments are only used at API boundaries.
 */

export type Seg = { t: 'k'; k: string } | { t: 'i'; i: number };

export interface PNode {
  seg: Seg;
  up: PNode | null;
  depth: number; // depth of this node (root's child = 1); root itself is `null`
  id: number; // intern id assigned by RecordDecoder; 0 = not interned
}

export type Path = PNode | null;

export function pushSeg(up: Path, seg: Seg): PNode {
  return { seg, up, depth: (up ? up.depth : 0) + 1, id: 0 };
}

export function pathDepth(p: Path): number {
  return p ? p.depth : 0;
}

export function toArray(p: Path): Seg[] {
  const out: Seg[] = [];
  let cur = p;
  while (cur) {
    out.push(cur.seg);
    cur = cur.up;
  }
  out.reverse();
  return out;
}

export function fromArray(segs: Seg[]): Path {
  let p: Path = null;
  for (const s of segs) p = pushSeg(p, s);
  return p;
}

/** Segments of `p` below `uptoDepth`, root-first. */
export function suffixArray(p: Path, uptoDepth: number): Seg[] {
  const out: Seg[] = [];
  let cur = p;
  while (cur && cur.depth > uptoDepth) {
    out.push(cur.seg);
    cur = cur.up;
  }
  out.reverse();
  return out;
}

export function segEqual(a: Seg, b: Seg): boolean {
  if (a.t !== b.t) return false;
  return a.t === 'k' ? a.k === (b as { t: 'k'; k: string }).k : a.i === (b as { t: 'i'; i: number }).i;
}

/** Total order: key segments sort before index segments; prefix sorts first. */
export function compareSegs(a: Seg, b: Seg): number {
  if (a.t !== b.t) return a.t === 'k' ? -1 : 1;
  if (a.t === 'k') {
    const bk = (b as { t: 'k'; k: string }).k;
    return a.k < bk ? -1 : a.k > bk ? 1 : 0;
  }
  return a.i - (b as { t: 'i'; i: number }).i;
}

export function comparePaths(a: Path, b: Path): number {
  const aa = toArray(a);
  const bb = toArray(b);
  const n = Math.min(aa.length, bb.length);
  for (let i = 0; i < n; i++) {
    const c = compareSegs(aa[i], bb[i]);
    if (c !== 0) return c;
  }
  return aa.length - bb.length;
}

export function equalPaths(a: Path, b: Path): boolean {
  let x = a;
  let y = b;
  while (x && y) {
    if (x === y) return true; // shared structure (interned decoder paths)
    if (!segEqual(x.seg, y.seg)) return false;
    x = x.up;
    y = y.up;
  }
  return x === y;
}

export function isPrefix(pre: Path, p: Path): boolean {
  const dp = pathDepth(pre);
  const dq = pathDepth(p);
  if (dp > dq) return false;
  let cur = p;
  for (let i = dq; i > dp; i--) cur = cur!.up;
  let a = pre;
  let b = cur;
  while (a && b) {
    if (a === b) return true;
    if (!segEqual(a.seg, b.seg)) return false;
    a = a.up;
    b = b.up;
  }
  return a === null && b === null;
}

const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

export function formatPath(p: Path): string {
  let out = '$';
  for (const seg of toArray(p)) {
    if (seg.t === 'i') out += `[${seg.i}]`;
    else if (IDENT.test(seg.k)) out += `.${seg.k}`;
    else out += `[${JSON.stringify(seg.k)}]`;
  }
  return out;
}
