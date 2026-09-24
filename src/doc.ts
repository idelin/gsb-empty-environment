import { idToString, compareIds } from './id.ts';
import type { ElementId } from './id.ts';

/** Operations are the only messages exchanged between replicas. */
export type Op = InsertOp | DeleteOp;

export interface InsertOp {
  readonly type: 'insert';
  readonly id: ElementId;
  /** Parent element this one is anchored after; null anchors at the sequence head. */
  readonly parent: ElementId | null;
  /** A single Unicode code point (1 or 2 UTF-16 code units). */
  readonly value: string;
}

export interface DeleteOp {
  readonly type: 'delete';
  readonly id: ElementId;
}

interface Element {
  id: ElementId;
  parent: ElementId | null;
  value: string;
  tombstone: boolean;
}

/** Key of the virtual root element. Real ids are "replica:counter" and never collide with it. */
const ROOT = '';

export interface Snapshot {
  elements: Array<Element & { key: string }>;
  pending: Record<string, InsertOp[]>;
  deletesAhead: string[];
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` is a valid UTF-16 boundary of `text` (never inside a surrogate pair). */
export function isUtf16Boundary(text: string, offset: number): boolean {
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) return false;
  if (offset > 0 && offset < text.length) {
    const prev = text.charCodeAt(offset - 1);
    const next = text.charCodeAt(offset);
    if (isHighSurrogate(prev) && isLowSurrogate(next)) return false;
  }
  return true;
}

function assertBoundary(text: string, offset: number): void {
  if (!isUtf16Boundary(text, offset)) {
    throw new RangeError(`offset ${offset} is not a UTF-16 boundary (would split a surrogate pair)`);
  }
}

/**
 * Offline-first text sequence with stable element ids.
 *
 * - Inserts name their causal dependency (parent). Ops whose parent has not
 *   arrived are buffered, never re-anchored to the head.
 * - Deletes are idempotent and kept as tombstones; a delete that arrives
 *   before its insert is remembered and applied when the insert lands.
 * - merge() is a set union with OR-ed tombstones: commutative, associative,
 *   idempotent.
 * - gc() only collects leaf tombstones covered by every replica's watermark.
 */
export class TextDoc {
  readonly replica: string;
  private counter = 0;
  private readonly elements = new Map<string, Element>();
  private readonly children = new Map<string, string[]>();
  private readonly pending = new Map<string, InsertOp[]>();
  private readonly deletesAhead = new Set<string>();

  constructor(replica: string) {
    this.replica = replica;
  }

  // ---------------------------------------------------------------- local API

  /** Visible text. Offsets exposed to callers are UTF-16 code units. */
  getText(): string {
    let out = '';
    const walk = (parentKey: string): void => {
      for (const key of this.children.get(parentKey) ?? []) {
        const el = this.elements.get(key)!;
        if (!el.tombstone) out += el.value;
        walk(key);
      }
    };
    walk(ROOT);
    return out;
  }

  /** Visible length in UTF-16 code units. */
  get length(): number {
    return this.getText().length;
  }

  /** Insert `text` at UTF-16 `offset`; returns the ops to broadcast. */
  insertAt(offset: number, text: string): InsertOp[] {
    const current = this.getText();
    assertBoundary(current, offset);
    const anchor = this.elementEndingAt(offset);
    const ops: InsertOp[] = [];
    let parent: ElementId | null = anchor ? anchor.id : null;
    for (const codePoint of text) {
      const id: ElementId = { replica: this.replica, counter: ++this.counter };
      const op: InsertOp = { type: 'insert', id, parent, value: codePoint };
      this.applyInsert(op);
      ops.push(op);
      parent = id;
    }
    return ops;
  }

  /** Delete `len` UTF-16 code units starting at `offset`; returns the ops to broadcast. */
  deleteAt(offset: number, len: number): DeleteOp[] {
    const current = this.getText();
    assertBoundary(current, offset);
    assertBoundary(current, offset + len);
    const ops: DeleteOp[] = [];
    let pos = 0;
    for (const el of this.visibleElements()) {
      const start = pos;
      const end = pos + el.value.length;
      pos = end;
      if (start >= offset && end <= offset + len) {
        const op: DeleteOp = { type: 'delete', id: el.id };
        this.applyDelete(op);
        ops.push(op);
      }
    }
    return ops;
  }

  // ------------------------------------------------------------- op handling

  apply(op: Op): void {
    if (op.type === 'insert') this.applyInsert(op);
    else this.applyDelete(op);
  }

  applyInsert(op: InsertOp): void {
    const key = idToString(op.id);
    if (this.elements.has(key)) return; // duplicate delivery: no change
    const parentKey = op.parent ? idToString(op.parent) : ROOT;
    if (op.parent !== null && !this.elements.has(parentKey)) {
      // Causal dependency missing: buffer, do NOT re-anchor.
      const queue = this.pending.get(parentKey) ?? [];
      if (!queue.some((p) => idToString(p.id) === key)) queue.push(op);
      this.pending.set(parentKey, queue);
      return;
    }
    this.integrate(op);
  }

  applyDelete(op: DeleteOp): void {
    const key = idToString(op.id);
    const el = this.elements.get(key);
    if (el) {
      el.tombstone = true; // idempotent
    } else {
      this.deletesAhead.add(key); // delete before insert: remember it
    }
  }

  private integrate(op: InsertOp): void {
    const key = idToString(op.id);
    const el: Element = {
      id: op.id,
      parent: op.parent,
      value: op.value,
      tombstone: this.deletesAhead.has(key),
    };
    // The pre-arrived delete is now materialized as the element's tombstone.
    this.deletesAhead.delete(key);
    this.elements.set(key, el);
    const parentKey = op.parent ? idToString(op.parent) : ROOT;
    const siblings = this.children.get(parentKey) ?? [];
    siblings.push(key);
    // RGA-style: newer (larger) ids sort first, so a fresh insert lands
    // immediately after its parent. Total order => deterministic everywhere.
    siblings.sort((a, b) => compareIds(this.elements.get(b)!.id, this.elements.get(a)!.id));
    this.children.set(parentKey, siblings);
    // Drain ops that were waiting on this element.
    const unblocked = this.pending.get(key);
    if (unblocked) {
      this.pending.delete(key);
      for (const child of unblocked) this.applyInsert(child);
    }
  }

  // -------------------------------------------------------------------- merge

  /** Union `other` into this doc. Commutative, associative, idempotent. */
  merge(other: TextDoc): void {
    for (const [key, el] of other.elements) {
      const mine = this.elements.get(key);
      if (!mine) {
        const copy: Element = { ...el, id: { ...el.id }, parent: el.parent ? { ...el.parent } : null };
        this.elements.set(key, copy);
        const parentKey = copy.parent ? idToString(copy.parent) : ROOT;
        const siblings = this.children.get(parentKey) ?? [];
        siblings.push(key);
        siblings.sort((a, b) => compareIds(this.elements.get(b)!.id, this.elements.get(a)!.id));
        this.children.set(parentKey, siblings);
      } else {
        mine.tombstone = mine.tombstone || el.tombstone;
      }
    }
    for (const key of other.deletesAhead) this.deletesAhead.add(key);
    for (const ops of other.pending.values()) {
      for (const op of ops) {
        const key = idToString(op.id);
        if (this.elements.has(key)) continue;
        const parentKey = op.parent ? idToString(op.parent) : ROOT;
        const queue = this.pending.get(parentKey) ?? [];
        if (!queue.some((p) => idToString(p.id) === key)) queue.push(op);
        this.pending.set(parentKey, queue);
      }
    }
    // Close over newly satisfiable dependencies.
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const [parentKey, ops] of [...this.pending]) {
        if (parentKey !== ROOT && this.elements.has(parentKey)) {
          this.pending.delete(parentKey);
          for (const op of ops) this.applyInsert(op);
          progressed = true;
        }
      }
    }
  }

  /** Pure merge: merged(a, b) === merged(b, a). */
  static merged(a: TextDoc, b: TextDoc): TextDoc {
    const out = TextDoc.from(a.snapshot(), a.replica);
    out.merge(b);
    return out;
  }

  // ----------------------------------------------------------------------- GC

  /**
   * Highest counter per replica such that every op up to it is integrated
   * here (contiguous prefix). This is what the replica may safely ack.
   */
  watermark(): Map<string, number> {
    const seen = new Map<string, Set<number>>();
    for (const el of this.elements.values()) {
      let set = seen.get(el.id.replica);
      if (!set) seen.set(el.id.replica, (set = new Set()));
      set.add(el.id.counter);
    }
    const wm = new Map<string, number>();
    for (const [replica, counters] of seen) {
      let n = 0;
      while (counters.has(n + 1)) n++;
      wm.set(replica, n);
    }
    return wm;
  }

  /**
   * Collect tombstones that every replica has acknowledged.
   * `acks` maps replica -> that replica's watermark (replica -> counter) and
   * MUST contain one entry per replica in the system: the keys declare the
   * membership, so a replica that has acked nothing yet must be present with
   * an empty map. The local replica's own watermark is folded in
   * automatically. Only leaf tombstones are collected, so parents of live
   * elements are never removed. Returns the collected ids.
   */
  gc(acks: ReadonlyMap<string, ReadonlyMap<string, number>>): ElementId[] {
    const vectors = [...acks.values(), this.watermark()];
    const replicas = new Set<string>();
    for (const v of vectors) for (const r of v.keys()) replicas.add(r);
    const min = new Map<string, number>();
    for (const r of replicas) {
      min.set(r, Math.min(...vectors.map((v) => v.get(r) ?? 0)));
    }
    const collected: ElementId[] = [];
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const [key, el] of [...this.elements]) {
        if (!el.tombstone) continue;
        if (el.id.counter > (min.get(el.id.replica) ?? 0)) continue;
        if ((this.children.get(key) ?? []).length > 0) continue; // not a leaf
        this.elements.delete(key);
        this.deletesAhead.delete(key);
        const parentKey = el.parent ? idToString(el.parent) : ROOT;
        const siblings = this.children.get(parentKey);
        if (siblings) {
          const i = siblings.indexOf(key);
          if (i >= 0) siblings.splice(i, 1);
        }
        this.children.delete(key);
        collected.push(el.id);
        progressed = true;
      }
    }
    return collected;
  }

  // ---------------------------------------------------------------- snapshots

  snapshot(): Snapshot {
    const elements = [...this.elements.entries()]
      .map(([key, el]) => ({ key, ...el }))
      .sort((a, b) => (a.key < b.key ? -1 : 1));
    const pending: Record<string, InsertOp[]> = {};
    for (const [k, ops] of this.pending) {
      pending[k] = [...ops].sort((a, b) => compareIds(a.id, b.id));
    }
    return { elements, pending, deletesAhead: [...this.deletesAhead].sort() };
  }

  static from(snapshot: Snapshot, replica: string): TextDoc {
    const doc = new TextDoc(replica);
    for (const { key: _key, ...el } of snapshot.elements) {
      doc.elements.set(idToString(el.id), { ...el });
      const parentKey = el.parent ? idToString(el.parent) : ROOT;
      const siblings = doc.children.get(parentKey) ?? [];
      siblings.push(idToString(el.id));
      doc.children.set(parentKey, siblings);
    }
    for (const siblings of doc.children.values()) {
      siblings.sort((a, b) => compareIds(doc.elements.get(b)!.id, doc.elements.get(a)!.id));
    }
    for (const [k, ops] of Object.entries(snapshot.pending)) doc.pending.set(k, [...ops]);
    for (const key of snapshot.deletesAhead) doc.deletesAhead.add(key);
    return doc;
  }

  // ----------------------------------------------------------------- helpers

  private visibleElements(): Element[] {
    const out: Element[] = [];
    const walk = (parentKey: string): void => {
      for (const key of this.children.get(parentKey) ?? []) {
        const el = this.elements.get(key)!;
        if (!el.tombstone) out.push(el);
        walk(key);
      }
    };
    walk(ROOT);
    return out;
  }

  /** The visible element whose UTF-16 range ends exactly at `offset`, if any. */
  private elementEndingAt(offset: number): Element | null {
    if (offset === 0) return null;
    let pos = 0;
    for (const el of this.visibleElements()) {
      pos += el.value.length;
      if (pos === offset) return el;
    }
    return null;
  }
}
