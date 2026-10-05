/**
 * Test helpers: an independent array-of-scalars model used to cross-check
 * the rope implementation, plus rope invariant validation.
 */
import { Node, NodeKind, LEAF_MAX } from "../src/rope";
import { countScalars, utf8ByteLength, validateString } from "../src/utf8";

declare function require(name: string): any;
const util = require("util");
export const textEncoder = new util.TextEncoder();
export const textDecoder = new util.TextDecoder("utf-8", { fatal: true });

export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error("assertion failed: " + msg);
}

export function assertEq(actual: unknown, expected: unknown, msg: string): void {
  if (actual !== expected) {
    throw new Error(msg + ": expected " + JSON.stringify(expected) + ", got " + JSON.stringify(actual));
  }
}

/** Deterministic PRNG (mulberry32). */
export function makeRng(seed: number): () => number {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Independent UTF-8 byte length of one code point. */
export function utf8LenOfCodePoint(cp: number): number {
  if (cp < 0x80) return 1;
  if (cp < 0x800) return 2;
  if (cp < 0x10000) return 3;
  return 4;
}

interface ModelEntry {
  parent: number | null;
  children: number[];
  text: string;
}

/**
 * Independent model: document = array of scalars (splice-based), history =
 * plain tree of full-text copies. Mirrors TextEditor's id assignment.
 */
export class Model {
  private entries = new Map<number, ModelEntry>();
  current = 0;
  private nextId = 1;

  constructor(initial = "") {
    this.entries.set(0, { parent: null, children: [], text: initial });
  }

  text(): string {
    return this.must(this.current).text;
  }

  scalarLength(): number {
    return Array.from(this.text()).length;
  }

  byteLength(): number {
    let n = 0;
    for (const ch of this.text()) {
      n += utf8LenOfCodePoint(ch.codePointAt(0) as number);
    }
    return n;
  }

  insert(offset: number, s: string): void {
    if (s.length === 0) return;
    const arr = Array.from(this.text());
    arr.splice(offset, 0, ...Array.from(s));
    this.commit(arr.join(""));
  }

  delete(offset: number, length: number): void {
    if (length === 0) return;
    const arr = Array.from(this.text());
    arr.splice(offset, length);
    this.commit(arr.join(""));
  }

  snapshot(): number {
    return this.current;
  }

  undo(): boolean {
    const e = this.must(this.current);
    if (e.parent === null) return false;
    this.current = e.parent;
    return true;
  }

  redo(): boolean {
    const e = this.must(this.current);
    if (e.children.length === 0) return false;
    this.current = e.children[e.children.length - 1];
    return true;
  }

  checkout(id: number): void {
    if (!this.entries.has(id)) throw new Error("model: unknown id " + id);
    this.current = id;
  }

  release(id: number): void {
    const e = this.must(id);
    if (id === this.current) throw new Error("model: release current");
    if (e.children.length > 0) throw new Error("model: release non-leaf");
    if (e.parent !== null) {
      const sib = this.must(e.parent).children;
      sib.splice(sib.indexOf(id), 1);
    }
    this.entries.delete(id);
  }

  /** Live snapshot ids (for random checkout/release targets). */
  ids(): number[] {
    return Array.from(this.entries.keys());
  }

  isLeaf(id: number): boolean {
    return this.must(id).children.length === 0;
  }

  private commit(text: string): void {
    const parent = this.must(this.current);
    const id = this.nextId++;
    this.entries.set(id, { parent: this.current, children: [], text });
    parent.children.push(id);
    this.current = id;
  }

  private must(id: number): ModelEntry {
    const e = this.entries.get(id);
    if (!e) throw new Error("model: corrupted, missing " + id);
    return e;
  }
}

/**
 * Validates rope invariants and recomputes aggregates from scratch:
 * - leaf: non-empty, <= LEAF_MAX scalars, bytes/scalars match text,
 *   text is a valid scalar sequence (no split encodings)
 * - branch: bytes/scalars/height match children, AVL balance holds
 */
export function checkRope(n: Node | null): { bytes: number; scalars: number } {
  if (!n) return { bytes: 0, scalars: 0 };
  if (n.kind === NodeKind.Leaf) {
    assert(n.text.length > 0, "leaf must not be empty");
    assert(n.scalars <= LEAF_MAX, "leaf exceeds LEAF_MAX");
    assertEq(n.scalars, countScalars(n.text), "leaf scalar count");
    assertEq(n.bytes, utf8ByteLength(n.text), "leaf byte count");
    validateString(n.text); // throws if a surrogate pair was cut
    return { bytes: n.bytes, scalars: n.scalars };
  }
  const l = checkRope(n.left);
  const r = checkRope(n.right);
  assertEq(n.bytes, l.bytes + r.bytes, "branch byte aggregate");
  assertEq(n.scalars, l.scalars + r.scalars, "branch scalar aggregate");
  assertEq(n.height, Math.max(nodeHeight(n.left), nodeHeight(n.right)) + 1, "branch height");
  assert(
    Math.abs(nodeHeight(n.left) - nodeHeight(n.right)) <= 1,
    "AVL balance violated"
  );
  return { bytes: n.bytes, scalars: n.scalars };
}

function nodeHeight(n: Node): number {
  return n.kind === NodeKind.Leaf ? 0 : n.height;
}

/** Collects the identity set of all nodes under a root. */
export function nodeSet(root: Node | null): Set<Node> {
  const seen = new Set<Node>();
  const stack: Node[] = [];
  if (root) stack.push(root);
  while (stack.length > 0) {
    const n = stack.pop() as Node;
    if (seen.has(n)) continue;
    seen.add(n);
    if (n.kind === NodeKind.Branch) {
      stack.push(n.left);
      stack.push(n.right);
    }
  }
  return seen;
}
