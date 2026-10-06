// Debug object model: issues stable reference handles for debuggee objects
// and serves paged, key-sorted property listings on expansion.
//
// Core guarantees:
// - Identity: a handle id denotes one specific object. Shared or cyclic
//   objects are interned to the SAME id within a pause generation; they are
//   never copied into independent objects by expansion.
// - Generations: every pause/resume transition invalidates all previously
//   issued handles. Resolving a stale handle throws StaleHandleError.
// - Laziness: accessor (getter) properties are never invoked by default;
//   callers must opt in per expansion or per property.

import { VNode } from './virtualGraph.ts';
import type { VProp, VValue } from './virtualGraph.ts';

export interface ObjectHandle {
  readonly id: string;
  readonly generation: number;
  readonly className: string;
  readonly preview: string;
}

export interface PropertyEntry {
  readonly name: string;
  readonly kind: 'data' | 'accessor';
  readonly preview: string;
  // Handle id when the value is an object; null for primitives and for
  // accessors that have not been invoked.
  readonly refId: string | null;
  readonly getterInvoked: boolean;
}

export interface PropertyPage {
  readonly handleId: string;
  readonly offset: number;
  readonly total: number;
  readonly items: PropertyEntry[];
  readonly hasMore: boolean;
}

export interface ExpandOptions {
  offset?: number;
  count?: number;
  invokeGetters?: boolean;
}

export class StaleHandleError extends Error {
  readonly handleId: string;
  readonly handleGeneration: number;
  readonly currentGeneration: number;

  constructor(handleId: string, handleGeneration: number, currentGeneration: number) {
    super(
      `handle ${handleId} was issued in pause generation ${handleGeneration} ` +
        `but the current generation is ${currentGeneration}`,
    );
    this.name = 'StaleHandleError';
    this.handleId = handleId;
    this.handleGeneration = handleGeneration;
    this.currentGeneration = currentGeneration;
  }
}

export class UnknownHandleError extends Error {
  readonly handleId: string;

  constructor(handleId: string) {
    super(`unknown handle: ${handleId}`);
    this.name = 'UnknownHandleError';
    this.handleId = handleId;
  }
}

const HANDLE_PATTERN = /^obj@(\d+):(\d+)$/;
const DEFAULT_PAGE_SIZE = 100;

// Array-index keys sort numerically before all string keys, which sort by
// code unit. This keeps paging deterministic regardless of insertion order.
function arrayIndexOf(name: string): number | null {
  if (name === '') return null;
  const n = Number(name);
  if (!Number.isInteger(n) || n < 0 || n >= 2 ** 32 - 1) return null;
  return String(n) === name ? n : null;
}

export function comparePropertyKeys(a: string, b: string): number {
  const ia = arrayIndexOf(a);
  const ib = arrayIndexOf(b);
  if (ia !== null && ib !== null) return ia - ib;
  if (ia !== null) return -1;
  if (ib !== null) return 1;
  return a < b ? -1 : a > b ? 1 : 0;
}

export class DebugModel {
  private generation = 0;
  private paused = false;
  private nextLocalId = 1;
  private readonly idByNode = new Map<VNode, string>();
  private readonly nodeById = new Map<string, VNode>();

  get currentGeneration(): number {
    return this.generation;
  }

  // Starts a new pause generation and returns handles for the given root
  // objects. All handles from earlier generations become stale.
  pause(roots: VNode[]): ObjectHandle[] {
    this.generation += 1;
    this.paused = true;
    this.idByNode.clear();
    this.nodeById.clear();
    return roots.map((root) => this.handleFor(root));
  }

  // Resumes execution; every outstanding handle becomes stale.
  resume(): void {
    this.paused = false;
    this.idByNode.clear();
    this.nodeById.clear();
  }

  isValid(handleId: string): boolean {
    try {
      this.resolve(handleId);
      return true;
    } catch {
      return false;
    }
  }

  // Returns a page of the object's own properties, sorted by key.
  expand(handleId: string, options: ExpandOptions = {}): PropertyPage {
    const node = this.resolve(handleId);
    const offset = options.offset ?? 0;
    const count = options.count ?? DEFAULT_PAGE_SIZE;
    const invokeGetters = options.invokeGetters ?? false;

    const names = [...node.props.keys()].sort(comparePropertyKeys);
    const slice = names.slice(offset, offset + count);
    const items = slice.map((name) =>
      this.entryFor(name, node.props.get(name)!, invokeGetters),
    );

    return {
      handleId,
      offset,
      total: names.length,
      items,
      hasMore: offset + items.length < names.length,
    };
  }

  // Explicitly invokes a single accessor property. This is the only way to
  // run a getter besides passing invokeGetters to expand().
  evaluateGetter(handleId: string, name: string): PropertyEntry {
    const node = this.resolve(handleId);
    const prop = node.props.get(name);
    if (!prop) {
      throw new UnknownHandleError(`${handleId} has no property ${name}`);
    }
    if (prop.kind !== 'accessor') {
      throw new Error(`property ${name} of ${handleId} is a data property`);
    }
    return this.dataEntry(name, prop.get(), 'accessor', true);
  }

  // Interns a node: the same object always yields the same handle id within
  // a generation, so shared and cyclic references stay references.
  private handleFor(node: VNode): ObjectHandle {
    let id = this.idByNode.get(node);
    if (id === undefined) {
      id = `obj@${this.generation}:${this.nextLocalId++}`;
      this.idByNode.set(node, id);
      this.nodeById.set(id, node);
    }
    return {
      id,
      generation: this.generation,
      className: node.className,
      preview: `${node.className}#${node.nodeId}`,
    };
  }

  private resolve(handleId: string): VNode {
    const match = HANDLE_PATTERN.exec(handleId);
    if (!match) throw new UnknownHandleError(handleId);
    const handleGeneration = Number(match[1]);
    if (!this.paused || handleGeneration !== this.generation) {
      throw new StaleHandleError(handleId, handleGeneration, this.generation);
    }
    const node = this.nodeById.get(handleId);
    if (!node) throw new UnknownHandleError(handleId);
    return node;
  }

  private entryFor(name: string, prop: VProp, invokeGetters: boolean): PropertyEntry {
    if (prop.kind === 'accessor') {
      if (!invokeGetters) {
        return {
          name,
          kind: 'accessor',
          preview: '(getter)',
          refId: null,
          getterInvoked: false,
        };
      }
      return this.dataEntry(name, prop.get(), 'accessor', true);
    }
    return this.dataEntry(name, prop.value, 'data', false);
  }

  private dataEntry(
    name: string,
    value: VValue,
    kind: 'data' | 'accessor',
    getterInvoked: boolean,
  ): PropertyEntry {
    if (value instanceof VNode) {
      const handle = this.handleFor(value);
      return {
        name,
        kind,
        preview: handle.preview,
        refId: handle.id,
        getterInvoked,
      };
    }
    return {
      name,
      kind,
      preview: previewPrimitive(value),
      refId: null,
      getterInvoked,
    };
  }
}

function previewPrimitive(value: VValue): string {
  if (typeof value === 'string') return JSON.stringify(value);
  if (value === undefined) return 'undefined';
  if (value === null) return 'null';
  return String(value);
}
