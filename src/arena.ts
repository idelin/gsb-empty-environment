/**
 * Arena: reference-counted registry of every live container node.
 *
 * The memory ledger counts *retained tree nodes*: each object/array node
 * weighs 1 (itself) + 1 per scalar child slot. Container children are
 * counted as their own nodes, so shared structure between versions is
 * metered exactly once. Invariant: retained === allocatedTotal - freedTotal.
 */

import { isContainer } from './json.ts';
import type { JsonValue } from './json.ts';

interface NodeMeta {
  refs: number;
  weight: number;
}

export class Arena {
  private meta = new Map<object, NodeMeta>();
  retained = 0;
  allocatedTotal = 0;
  freedTotal = 0;

  static weightOf(node: object): number {
    let weight = 1;
    for (const child of Object.values(node)) {
      if (!isContainer(child)) weight += 1;
    }
    return weight;
  }

  has(node: object): boolean {
    return this.meta.has(node);
  }

  /** Register a freshly built node (refs start at 0) and freeze it. */
  alloc<T extends object>(node: T): T {
    if (this.meta.has(node)) throw new Error('arena: node already tracked');
    const weight = Arena.weightOf(node);
    this.meta.set(node, { refs: 0, weight });
    this.retained += weight;
    this.allocatedTotal += weight;
    Object.freeze(node);
    return node;
  }

  retainValue(value: JsonValue): void {
    if (isContainer(value)) this.retain(value);
  }

  releaseValue(value: JsonValue): void {
    if (isContainer(value)) this.release(value);
  }

  retain(node: object): void {
    const entry = this.meta.get(node);
    if (!entry) throw new Error('arena: attempt to retain a foreign node');
    entry.refs += 1;
  }

  release(node: object): void {
    const entry = this.meta.get(node);
    if (!entry) throw new Error('arena: attempt to release a foreign node');
    entry.refs -= 1;
    if (entry.refs < 0) throw new Error('arena: refcount underflow');
    if (entry.refs > 0) return;
    this.meta.delete(node);
    this.retained -= entry.weight;
    this.freedTotal += entry.weight;
    for (const child of Object.values(node)) {
      this.releaseValue(child as JsonValue);
    }
  }
}
