"use strict";
// Compiled from src/history.ts (types stripped; logic identical).
Object.defineProperty(exports, "__esModule", { value: true });
exports.History = exports.deepEqual = void 0;

function deepEqual(a, b) {
  if (Object.is(a, b)) return true;
  if (
    typeof a !== "object" || a === null ||
    typeof b !== "object" || b === null
  ) {
    return false;
  }
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  for (const key of keysA) {
    if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
    if (!deepEqual(a[key], b[key])) return false;
  }
  return true;
}
exports.deepEqual = deepEqual;

class History {
  constructor(initial, options = {}) {
    this.mergeTarget = null;
    this.nextId = 1;
    this.nextSeq = 1;
    this.count = 0;
    this.capacity = options.capacity ?? Number.POSITIVE_INFINITY;
    this.equals = options.equals ?? deepEqual;
    this.root = {
      id: 0,
      parent: null,
      children: [],
      snapshot: initial,
      label: null,
      mergeable: false,
      seq: 0,
    };
    this.current = this.root;
    this.savedSnapshot = initial;
  }

  get value() {
    return this.current.snapshot;
  }

  get size() {
    return this.count;
  }

  get depth() {
    let depth = 0;
    let node = this.current;
    while (node.parent !== null) {
      depth += 1;
      node = node.parent;
    }
    return depth;
  }

  canUndo() {
    return this.current !== this.root;
  }

  canRedo() {
    return this.current.children.length > 0;
  }

  branches() {
    return this.current.children.length;
  }

  commit(snapshot, options = {}) {
    const label = options.label ?? null;
    const mergeable = options.mergeable ?? false;
    const target = this.current;
    const canMerge =
      mergeable &&
      label !== null &&
      this.mergeTarget === target &&
      target !== this.root &&
      target.mergeable &&
      target.label === label;
    if (canMerge) {
      target.snapshot = snapshot;
      return;
    }
    const node = {
      id: this.nextId++,
      parent: target,
      children: [],
      snapshot,
      label,
      mergeable,
      seq: this.nextSeq++,
    };
    target.children.push(node);
    this.current = node;
    this.mergeTarget = node;
    this.count += 1;
    this.evict();
  }

  undo() {
    if (this.current === this.root) return undefined;
    this.current = this.current.parent;
    this.mergeTarget = null;
    return this.current.snapshot;
  }

  redo(branch) {
    const children = this.current.children;
    if (children.length === 0) return undefined;
    const index = branch ?? children.length - 1;
    const next = children[index];
    if (next === undefined) return undefined;
    this.current = next;
    this.mergeTarget = null;
    return next.snapshot;
  }

  markSaved() {
    this.savedSnapshot = this.current.snapshot;
  }

  isDirty() {
    return !this.equals(this.current.snapshot, this.savedSnapshot);
  }

  evict() {
    while (this.count > this.capacity) {
      if (this.root !== this.current && this.root.children.length === 1) {
        const promoted = this.root.children[0];
        promoted.parent = null;
        this.root = promoted;
        this.count -= 1;
        continue;
      }
      let victim = null;
      const stack = [this.root];
      while (stack.length > 0) {
        const node = stack.pop();
        if (node !== this.root && node !== this.current && node.children.length === 0) {
          if (victim === null || node.seq < victim.seq) victim = node;
        }
        for (const child of node.children) stack.push(child);
      }
      if (victim === null) break;
      const siblings = victim.parent.children;
      siblings.splice(siblings.indexOf(victim), 1);
      this.count -= 1;
    }
  }
}
exports.History = History;
