/**
 * Debug-time variable inspection model.
 *
 * Objects in the debuggee may be shared (reachable from many parents) or
 * cyclic. The model hands out opaque handles ("references") for expandable
 * values. Identity is preserved: the same object always maps to the same
 * reference id within one pause generation, so shared objects are never
 * duplicated into independent copies and cycles are observable by comparing
 * ids. Every new pause bumps the generation, which invalidates all handles
 * minted before it.
 */

export type NodeKind = 'primitive' | 'object' | 'getter';

export interface VariableNode {
  /** Property / element key. */
  name: string;
  /** Display string for the value. */
  value: string;
  /** Runtime-ish type label. */
  type: string;
  /** Opaque handle for expanding; 0 means "not expandable". */
  reference: number;
  /**
   * Stable identity of the underlying object within the current pause
   * generation. Two nodes with the same objectId denote the same object.
   */
  objectId?: number;
  kind: NodeKind;
  /** True when this child is one of its own ancestors (cycle). */
  cycle?: boolean;
}

export interface ExpandOptions {
  /** Offset into the key-sorted child list. Defaults to 0. */
  start?: number;
  /** Page size. Defaults to all remaining children. */
  count?: number;
  /** Invoke property getters. Defaults to false (getters are inert). */
  invokeGetters?: boolean;
  /**
   * objectIds of the expansion path leading to this handle. Children whose
   * objectId appears here are flagged with `cycle: true`.
   */
  ancestors?: readonly number[];
}

export interface ChildrenPage {
  nodes: VariableNode[];
  /** Total number of children before pagination. */
  total: number;
}

export class StaleHandleError extends Error {
  readonly code = 'STALE_HANDLE' as const;
  readonly reference: number;
  readonly handleGeneration: number;
  readonly currentGeneration: number;
  constructor(reference: number, handleGeneration: number, currentGeneration: number) {
    super(
      `Reference ${reference} belongs to pause generation ${handleGeneration}, ` +
        `but the current generation is ${currentGeneration}`,
    );
    this.name = 'StaleHandleError';
    this.reference = reference;
    this.handleGeneration = handleGeneration;
    this.currentGeneration = currentGeneration;
  }
}

export class InvalidHandleError extends Error {
  readonly code = 'INVALID_HANDLE' as const;
  readonly reference: number;
  constructor(reference: number) {
    super(`Unknown reference ${reference}`);
    this.name = 'InvalidHandleError';
    this.reference = reference;
  }
}

interface Handle {
  generation: number;
  objectId: number;
  target: object;
}

const MAX_SAFE_ARRAY_INDEX = 2 ** 32 - 2;

function arrayIndex(key: string): number | undefined {
  if (key === '' || !/^\d+$/.test(key)) return undefined;
  const n = Number(key);
  if (!Number.isSafeInteger(n) || n > MAX_SAFE_ARRAY_INDEX || String(n) !== key) {
    return undefined;
  }
  return n;
}

/** Integer-like keys first (numeric order), then string keys (code-unit order). */
export function compareKeys(a: string, b: string): number {
  const ai = arrayIndex(a);
  const bi = arrayIndex(b);
  if (ai !== undefined && bi !== undefined) return ai - bi;
  if (ai !== undefined) return -1;
  if (bi !== undefined) return 1;
  return a < b ? -1 : a > b ? 1 : 0;
}

function isExpandable(value: unknown): value is object {
  return (typeof value === 'object' && value !== null) || typeof value === 'function';
}

function typeOf(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'Array';
  if (typeof value === 'function') return 'function';
  if (typeof value === 'object') {
    const ctor = (value as { constructor?: { name?: string } }).constructor;
    return ctor && typeof ctor.name === 'string' && ctor.name !== '' ? ctor.name : 'Object';
  }
  return typeof value;
}

function preview(value: unknown): string {
  if (typeof value === 'function') {
    const name = (value as { name?: string }).name;
    return name ? `ƒ ${name}()` : 'ƒ ()';
  }
  if (Array.isArray(value)) return `Array(${value.length})`;
  if (typeof value === 'object' && value !== null) return `${typeOf(value)} {…}`;
  if (typeof value === 'string') return JSON.stringify(value);
  return String(value);
}

export class DebugVariableModel {
  private generation = 0;
  private nextHandleId = 1;
  private nextObjectId = 1;
  private handles = new Map<number, Handle>();
  private objectIds = new WeakMap<object, number>();
  private handleByObject = new WeakMap<object, number>();

  constructor() {
    this.beginPause();
  }

  /** Current pause generation. */
  get currentGeneration(): number {
    return this.generation;
  }

  /**
   * Start a new pause. Bumps the generation and invalidates every handle
   * minted under earlier generations; expanding them raises StaleHandleError.
   * Handles from the immediately previous generation are retained so stale
   * lookups can be diagnosed; anything older is pruned and reports as
   * InvalidHandleError.
   */
  beginPause(): number {
    this.generation += 1;
    for (const [id, handle] of this.handles) {
      if (handle.generation < this.generation - 1) this.handles.delete(id);
    }
    this.objectIds = new WeakMap();
    this.handleByObject = new WeakMap();
    this.nextObjectId = 1;
    return this.generation;
  }

  /** Register a root value (e.g. a scope) and get its node. */
  createRoot(name: string, value: unknown): VariableNode {
    return this.nodeForValue(name, value);
  }

  /** Expand a handle into a paginated, key-sorted page of children. */
  expand(reference: number, options: ExpandOptions = {}): ChildrenPage {
    const handle = this.handles.get(reference);
    if (!handle) throw new InvalidHandleError(reference);
    if (handle.generation !== this.generation) {
      throw new StaleHandleError(reference, handle.generation, this.generation);
    }

    const descriptors = Object.getOwnPropertyDescriptors(handle.target);
    const keys = Object.keys(descriptors).sort(compareKeys);

    const start = Math.max(0, options.start ?? 0);
    const count = options.count ?? keys.length - start;
    const pageKeys = keys.slice(start, start + Math.max(0, count));

    const nodes = pageKeys.map((key) =>
      this.nodeForDescriptor(key, descriptors[key]!, handle.target, options),
    );
    return { nodes, total: keys.length };
  }

  private objectIdFor(target: object): number {
    let id = this.objectIds.get(target);
    if (id === undefined) {
      id = this.nextObjectId++;
      this.objectIds.set(target, id);
    }
    return id;
  }

  private referenceFor(target: object): number {
    let id = this.handleByObject.get(target);
    if (id === undefined) {
      id = this.nextHandleId++;
      this.handles.set(id, {
        generation: this.generation,
        objectId: this.objectIdFor(target),
        target,
      });
      this.handleByObject.set(target, id);
    }
    return id;
  }

  private nodeForValue(
    name: string,
    value: unknown,
    ancestors?: readonly number[],
  ): VariableNode {
    if (!isExpandable(value)) {
      return { name, value: preview(value), type: typeOf(value), reference: 0, kind: 'primitive' };
    }
    const objectId = this.objectIdFor(value);
    const node: VariableNode = {
      name,
      value: preview(value),
      type: typeOf(value),
      reference: this.referenceFor(value),
      objectId,
      kind: 'object',
    };
    if (ancestors !== undefined && ancestors.includes(objectId)) {
      node.cycle = true;
    }
    return node;
  }

  private nodeForDescriptor(
    name: string,
    descriptor: PropertyDescriptor,
    owner: object,
    options: ExpandOptions,
  ): VariableNode {
    const isAccessor = descriptor.get !== undefined || descriptor.set !== undefined;
    if (isAccessor && !options.invokeGetters) {
      return {
        name,
        value: '(getter — not invoked)',
        type: 'accessor',
        reference: 0,
        kind: 'getter',
      };
    }
    let value: unknown;
    if (isAccessor) {
      try {
        value = descriptor.get ? descriptor.get.call(owner) : undefined;
      } catch (error) {
        return {
          name,
          value: `<getter threw: ${error instanceof Error ? error.message : String(error)}>`,
          type: 'accessor',
          reference: 0,
          kind: 'getter',
        };
      }
    } else {
      value = descriptor.value;
    }
    return this.nodeForValue(name, value, options.ancestors);
  }
}
