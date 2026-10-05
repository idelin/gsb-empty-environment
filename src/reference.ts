/**
 * ReferenceModel — a deliberately independent oracle for the store.
 *
 * It keeps a FULL deep copy of the state for every version (no structural
 * sharing, no refcounts, no eviction). If the store's persistent-tree
 * bookkeeping is correct, every retained store version must deep-equal the
 * reference history for that version. Mutation semantics mirror the store's
 * spec (auto-vivify missing containers, arrays append at index === length,
 * deletes require an existing path, scalars cannot be traversed).
 */

import type { JsonObject, JsonValue, Path } from './json.ts';

export type RefOp =
  | { kind: 'set'; path: Path; value: JsonValue }
  | { kind: 'delete'; path: Path };

export function deepCopy<T>(value: T): T {
  if (typeof value !== 'object' || value === null) return value;
  if (Array.isArray(value)) {
    const out: unknown[] = new Array(value.length);
    for (let i = 0; i < value.length; i += 1) out[i] = deepCopy(value[i]);
    return out as T;
  }
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(value)) {
    out[key] = deepCopy((value as Record<string, unknown>)[key]);
  }
  return out as T;
}

type AnyContainer = Record<string, unknown> | unknown[];

function mutatingSet(state: JsonValue, path: Path, value: JsonValue): void {
  let node: AnyContainer = state as AnyContainer;
  for (let i = 0; i < path.length - 1; i += 1) {
    const key = path[i];
    let child = (node as Record<string, unknown>)[key as string];
    if (child === undefined) {
      child = typeof path[i + 1] === 'number' ? [] : {};
      (node as Record<string, unknown>)[key as string] = child;
    } else if (typeof child !== 'object' || child === null) {
      throw new TypeError(`reference: path traverses a scalar (segment ${i})`);
    }
    node = child as AnyContainer;
  }
  const last = path[path.length - 1];
  if (Array.isArray(node)) {
    if (typeof last !== 'number' || last > node.length) {
      throw new RangeError(`reference: array index ${String(last)} out of bounds`);
    }
    node[last] = value;
  } else {
    if (typeof last !== 'string') throw new TypeError('reference: object requires a string key');
    (node as Record<string, unknown>)[last] = value;
  }
}

function mutatingDelete(state: JsonValue, path: Path): void {
  let node: AnyContainer = state as AnyContainer;
  for (let i = 0; i < path.length - 1; i += 1) {
    const child = (node as Record<string, unknown>)[path[i] as string];
    if (typeof child !== 'object' || child === null) {
      throw new TypeError(`reference: path traverses a scalar (segment ${i})`);
    }
    node = child as AnyContainer;
  }
  const last = path[path.length - 1];
  if (Array.isArray(node)) {
    if (typeof last !== 'number' || last >= node.length) {
      throw new RangeError(`reference: array index ${String(last)} out of bounds`);
    }
    node.splice(last, 1);
  } else {
    if (!Object.prototype.hasOwnProperty.call(node, last)) {
      throw new RangeError(`reference: path not found: ${String(last)}`);
    }
    delete (node as Record<string, unknown>)[last as string];
  }
}

/** Apply one op to a mutable working state (used by the log generator). */
export function applyOpMutating(state: JsonObject, op: RefOp): void {
  if (op.kind === 'set') mutatingSet(state, op.path, deepCopy(op.value));
  else mutatingDelete(state, op.path);
}

export class ReferenceModel {
  readonly history = new Map<number, JsonObject>();
  headVersion = 0;
  private lastVersion = 0;

  constructor(initial: JsonObject) {
    this.history.set(0, deepCopy(initial));
  }

  commit(base: number, ops: RefOp[]): number {
    const baseState = this.history.get(base);
    if (!baseState) throw new Error(`reference: unknown base version ${base}`);
    const state = deepCopy(baseState);
    for (const op of ops) applyOpMutating(state, op);
    const version = ++this.lastVersion;
    this.history.set(version, state);
    this.headVersion = version;
    return version;
  }
}
