import type { JsonValue } from "./json.ts";
import { DataError } from "./errors.ts";

/**
 * Symbolic path segment: object hops use the key, array hops use the stable
 * entity id of the entry (NOT the index). Symbolic paths survive reorders,
 * so cached fragments and provenance stay valid when arrays are moved.
 */
export type SymSeg = { k: string } | { e: number };

export interface ArrEntry {
  eid: number;
  value: JNode;
}

export type JNode = null | boolean | number | string | JArr | JObj;
export interface JArr {
  kind: "arr";
  entries: ArrEntry[];
}
export interface JObj {
  kind: "obj";
  props: { [key: string]: JNode };
}

export function isArr(n: JNode): n is JArr {
  return typeof n === "object" && n !== null && (n as JArr).kind === "arr";
}
export function isObj(n: JNode): n is JObj {
  return typeof n === "object" && n !== null && (n as JObj).kind === "obj";
}

function hasOwn(o: { [key: string]: JNode }, k: string): boolean {
  return Object.prototype.hasOwnProperty.call(o, k);
}

function toIndex(seg: string): number | null {
  if (!/^(0|[1-9][0-9]*)$/.test(seg)) return null;
  const n = Number(seg);
  return Number.isSafeInteger(n) ? n : null;
}

/** Split "a.b.0" into segments; "" means the root. */
export function splitPath(path: string): string[] {
  if (path === "") return [];
  return path.split(".");
}

export interface ResolveResult {
  found: boolean;
  /** Deepest node that exists (the value itself when found). */
  value: JNode;
  /** Symbolic path of the deepest found node. */
  sym: SymSeg[];
  /** Concrete (index-based) segments of the deepest found node. */
  concrete: string[];
}

/**
 * Data store holding the input tree. Arrays wrap each element in an entry
 * with a stable entity id: identity is by id, never by content, so two
 * identical objects stay distinct and `move` preserves identity.
 *
 * All mutations are persistent (path-copying): unchanged subtrees keep their
 * object identity, which is what makes cheap cache validation possible, and
 * a snapshot is just the old root reference.
 */
export class Store {
  root: JNode;
  private eidCounter: number;

  constructor() {
    this.root = { kind: "obj", props: {} };
    this.eidCounter = 1;
  }

  wrap(json: JsonValue): JNode {
    if (json === null || typeof json !== "object") return json;
    if (Array.isArray(json)) {
      const entries = json.map((v) => ({ eid: this.eidCounter++, value: this.wrap(v) }));
      return { kind: "arr", entries };
    }
    const props: { [key: string]: JNode } = {};
    for (const k of Object.keys(json)) props[k] = this.wrap(json[k]);
    return { kind: "obj", props };
  }

  toJson(node: JNode): JsonValue {
    if (node === null || typeof node !== "object") return node;
    if (isArr(node)) return node.entries.map((e) => this.toJson(e.value));
    const out: { [key: string]: JsonValue } = {};
    for (const k of Object.keys(node.props)) out[k] = this.toJson(node.props[k]);
    return out;
  }

  snapshot(): JNode {
    return this.root;
  }

  restore(snap: JNode): void {
    this.root = snap;
  }

  /** Upsert. Object keys are created when missing; array indices must exist. */
  set(path: string, value: JsonValue): void {
    const segs = splitPath(path);
    this.root = this.setRec(this.root, segs, 0, this.wrap(value), path);
  }

  insert(path: string, index: number, value: JsonValue): void {
    const wrapped = this.wrap(value);
    const eid = this.eidCounter++;
    this.root = this.arrayOp(this.root, splitPath(path), 0, path, (entries) => {
      if (!Number.isInteger(index) || index < 0 || index > entries.length) {
        throw new DataError(`insert: index ${index} out of bounds (length ${entries.length})`, path);
      }
      const next = entries.slice();
      next.splice(index, 0, { eid, value: wrapped });
      return next;
    });
  }

  remove(path: string, index: number): void {
    this.root = this.arrayOp(this.root, splitPath(path), 0, path, (entries) => {
      if (!Number.isInteger(index) || index < 0 || index >= entries.length) {
        throw new DataError(`remove: index ${index} out of bounds (length ${entries.length})`, path);
      }
      const next = entries.slice();
      next.splice(index, 1);
      return next;
    });
  }

  /** Reorder an array element. The entry (and its entity id) moves as-is. */
  move(path: string, from: number, to: number): void {
    this.root = this.arrayOp(this.root, splitPath(path), 0, path, (entries) => {
      const ok = (i: number) => Number.isInteger(i) && i >= 0 && i < entries.length;
      if (!ok(from) || !ok(to)) {
        throw new DataError(`move: indices ${from} -> ${to} out of bounds (length ${entries.length})`, path);
      }
      const next = entries.slice();
      const [entry] = next.splice(from, 1);
      next.splice(to, 0, entry);
      return next;
    });
  }

  private setRec(node: JNode, segs: string[], i: number, newVal: JNode, path: string): JNode {
    if (i === segs.length) return newVal;
    const seg = segs[i];
    if (isObj(node)) {
      const props = { ...node.props };
      if (i === segs.length - 1) {
        props[seg] = newVal;
        return { kind: "obj", props };
      }
      if (!hasOwn(node.props, seg)) {
        throw new DataError(`set: key "${seg}" does not exist`, path);
      }
      props[seg] = this.setRec(node.props[seg], segs, i + 1, newVal, path);
      return { kind: "obj", props };
    }
    if (isArr(node)) {
      const idx = toIndex(seg);
      if (idx === null || idx >= node.entries.length) {
        throw new DataError(`set: index "${seg}" out of bounds`, path);
      }
      const entries = node.entries.slice();
      entries[idx] = { eid: entries[idx].eid, value: this.setRec(entries[idx].value, segs, i + 1, newVal, path) };
      return { kind: "arr", entries };
    }
    throw new DataError(`set: cannot traverse into ${JSON.stringify(node)} at "${seg}"`, path);
  }

  private arrayOp(
    node: JNode,
    segs: string[],
    i: number,
    path: string,
    fn: (entries: ArrEntry[]) => ArrEntry[],
  ): JNode {
    if (i === segs.length) {
      if (!isArr(node)) throw new DataError("array operation on a non-array value", path);
      return { kind: "arr", entries: fn(node.entries) };
    }
    const seg = segs[i];
    if (isObj(node)) {
      if (!hasOwn(node.props, seg)) throw new DataError(`key "${seg}" does not exist`, path);
      const props = { ...node.props };
      props[seg] = this.arrayOp(node.props[seg], segs, i + 1, path, fn);
      return { kind: "obj", props };
    }
    if (isArr(node)) {
      const idx = toIndex(seg);
      if (idx === null || idx >= node.entries.length) {
        throw new DataError(`index "${seg}" out of bounds`, path);
      }
      const entries = node.entries.slice();
      entries[idx] = { eid: entries[idx].eid, value: this.arrayOp(entries[idx].value, segs, i + 1, path, fn) };
      return { kind: "arr", entries };
    }
    throw new DataError(`cannot traverse into ${JSON.stringify(node)} at "${seg}"`, path);
  }

  /** Resolve a concrete (index-based) path from the root. */
  resolvePath(segs: string[]): ResolveResult {
    return this.walk(this.root, [], [], segs);
  }

  /** Resolve `segs` starting from an existing node/symbolic-path pair. */
  walk(node: JNode, sym: SymSeg[], concrete: string[], segs: string[]): ResolveResult {
    let cur = node;
    const s = sym.slice();
    const c = concrete.slice();
    for (const seg of segs) {
      if (isObj(cur)) {
        if (!hasOwn(cur.props, seg)) return { found: false, value: cur, sym: s, concrete: c };
        cur = cur.props[seg];
        s.push({ k: seg });
        c.push(seg);
      } else if (isArr(cur)) {
        const idx = toIndex(seg);
        if (idx === null || idx >= cur.entries.length) {
          return { found: false, value: cur, sym: s, concrete: c };
        }
        const entry = cur.entries[idx];
        cur = entry.value;
        s.push({ e: entry.eid });
        c.push(String(idx));
      } else {
        return { found: false, value: cur, sym: s, concrete: c };
      }
    }
    return { found: true, value: cur, sym: s, concrete: c };
  }

  /** Resolve a symbolic path (entity ids instead of indices) against current data. */
  resolveSym(sym: SymSeg[]): { found: boolean; value: JNode; concrete: string[] } {
    let cur = this.root;
    const c: string[] = [];
    for (const seg of sym) {
      if ("k" in seg) {
        if (!isObj(cur) || !hasOwn(cur.props, seg.k)) return { found: false, value: cur, concrete: c };
        cur = cur.props[seg.k];
        c.push(seg.k);
      } else {
        if (!isArr(cur)) return { found: false, value: cur, concrete: c };
        const idx = cur.entries.findIndex((en) => en.eid === seg.e);
        if (idx < 0) return { found: false, value: cur, concrete: c };
        cur = cur.entries[idx].value;
        c.push(String(idx));
      }
    }
    return { found: true, value: cur, concrete: c };
  }
}
