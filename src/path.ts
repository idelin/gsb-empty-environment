import { PathError } from "./errors.ts";

/**
 * Path segments are explicitly tagged so a numeric object key ("0") can never
 * be confused with an array index (0).
 */
export type Seg = { t: "k"; k: string } | { t: "i"; i: number };

/**
 * Paths are hash-consed into a linked structure so that:
 *  - memory stays O(depth) instead of O(depth^2) for deep chains,
 *  - prefix / equality checks are O(1) pointer comparisons after lifting.
 */
export interface PathNode {
  readonly id: number;
  readonly parent: PathNode | null;
  readonly seg: Seg | null;
  readonly depth: number;
}

export class PathInterner {
  readonly root: PathNode = { id: 0, parent: null, seg: null, depth: 0 };
  private readonly cache = new Map<string, PathNode>();
  private nextId = 1;

  intern(parent: PathNode, seg: Seg): PathNode {
    if (seg.t === "i" && (!Number.isInteger(seg.i) || seg.i < 0)) {
      throw new PathError("MalformedRecord", [], `invalid array index ${String(seg.i)}`);
    }
    const key = `${parent.id}|${segKey(seg)}`;
    const hit = this.cache.get(key);
    if (hit !== undefined) return hit;
    const node: PathNode = { id: this.nextId++, parent, seg, depth: parent.depth + 1 };
    this.cache.set(key, node);
    return node;
  }

  internPath(segs: Seg[]): PathNode {
    let node = this.root;
    for (const seg of segs) node = this.intern(node, seg);
    return node;
  }
}

export function segKey(seg: Seg): string {
  return seg.t === "k"
    ? `K${seg.k.length}:${seg.k}`
    : `I${String(seg.i).padStart(16, "0")}`;
}

export function compareSeg(a: Seg, b: Seg): number {
  if (a.t !== b.t) return a.t === "i" ? -1 : 1;
  if (a.t === "k") return a.k < b.k ? -1 : a.k > b.k ? 1 : 0;
  return a.i - b.i;
}

/** Total order: parents sort before children, siblings by segment order. */
export function comparePathNodes(a: PathNode, b: PathNode): number {
  if (a === b) return 0;
  let na: PathNode = a;
  let nb: PathNode = b;
  const segsA: Seg[] = [];
  const segsB: Seg[] = [];
  while (na.depth > nb.depth) {
    segsA.push(na.seg as Seg);
    na = na.parent as PathNode;
  }
  while (nb.depth > na.depth) {
    segsB.push(nb.seg as Seg);
    nb = nb.parent as PathNode;
  }
  while (na !== nb) {
    segsA.push(na.seg as Seg);
    segsB.push(nb.seg as Seg);
    na = na.parent as PathNode;
    nb = nb.parent as PathNode;
  }
  const len = Math.min(segsA.length, segsB.length);
  for (let k = 1; k <= len; k++) {
    const c = compareSeg(segsA[segsA.length - k] as Seg, segsB[segsB.length - k] as Seg);
    if (c !== 0) return c;
  }
  return segsA.length - segsB.length;
}

/** True when `b` equals `a` or lies in the subtree rooted at `a`. */
export function isPrefixNode(a: PathNode, b: PathNode): boolean {
  let n = b;
  while (n.depth > a.depth) n = n.parent as PathNode;
  return n === a;
}

export function isProperPrefixNode(a: PathNode, b: PathNode): boolean {
  return a !== b && isPrefixNode(a, b);
}

export function pathToArray(node: PathNode): Seg[] {
  const out: Seg[] = new Array(node.depth);
  let n = node;
  for (let i = node.depth - 1; i >= 0; i--) {
    out[i] = n.seg as Seg;
    n = n.parent as PathNode;
  }
  return out;
}

const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

export function formatPath(segs: Seg[]): string {
  let out = "$";
  for (const s of segs) {
    if (s.t === "i") out += `[${s.i}]`;
    else if (IDENT.test(s.k)) out += `.${s.k}`;
    else out += `[${JSON.stringify(s.k)}]`;
  }
  return out;
}
