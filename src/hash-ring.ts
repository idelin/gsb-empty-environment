/**
 * Topology-aware consistent hash ring with virtual nodes.
 *
 * Pure TypeScript, zero dependencies. Uses FNV-1a (32-bit) for point hashing
 * and a sorted array + binary search for O(log V) lookups, where V is the
 * total number of virtual nodes.
 */

export interface RingNode {
  id: string;
  /** Relative capacity; more virtual nodes are assigned to heavier nodes. */
  weight?: number;
  /** Failure-domain / rack / AZ label used by topology-aware replica spread. */
  zone?: string;
}

export interface OwnershipChange {
  point: number;
  from: string;
  to: string;
}

const VNODES_PER_WEIGHT = 160;

export function fnv1a(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

/** MurmurHash3 finalizer: strong avalanche so nearby inputs spread evenly. */
function fmix32(hash: number): number {
  hash ^= hash >>> 16;
  hash = Math.imul(hash, 0x85ebca6b);
  hash ^= hash >>> 13;
  hash = Math.imul(hash, 0xc2b2ae35);
  hash ^= hash >>> 16;
  return hash >>> 0;
}

/** Ring position of an arbitrary key. */
export function hashKey(input: string): number {
  return fmix32(fnv1a(input));
}

/** Ring position of one virtual node replica. */
function hashVnode(nodeId: string, replica: number): number {
  return fmix32(fnv1a(nodeId) ^ Math.imul(replica + 1, 0x9e3779b9));
}

interface NodeRecord {
  id: string;
  weight: number;
  zone: string;
}

export class HashRing {
  #nodes = new Map<string, NodeRecord>();
  /** Sorted vnode points, parallel to #owners. */
  #points: number[] = [];
  /** Owner node id for each point in #points. */
  #owners: string[] = [];
  #version = 0;

  constructor(nodes: RingNode[] = []) {
    for (const node of nodes) this.addNode(node);
  }

  get version(): number {
    return this.#version;
  }

  get size(): number {
    return this.#nodes.size;
  }

  get vnodeCount(): number {
    return this.#points.length;
  }

  nodeIds(): string[] {
    return [...this.#nodes.keys()];
  }

  hasNode(id: string): boolean {
    return this.#nodes.has(id);
  }

  zoneOf(id: string): string | undefined {
    return this.#nodes.get(id)?.zone;
  }

  addNode(node: RingNode): void {
    this.#nodes.set(node.id, {
      id: node.id,
      weight: node.weight ?? 1,
      zone: node.zone ?? "default",
    });
    this.#rebuild();
  }

  removeNode(id: string): boolean {
    const removed = this.#nodes.delete(id);
    if (removed) this.#rebuild();
    return removed;
  }

  /** Route a key to its owning node id. */
  lookup(key: string): string {
    if (this.#points.length === 0) {
      throw new Error("hash ring is empty");
    }
    return this.#owners[this.#locate(hashKey(key))];
  }

  /**
   * Topology-aware replica placement: returns up to `count` distinct nodes,
   * preferring nodes from distinct zones (failure domains) before reusing one.
   */
  lookupDistinct(key: string, count: number): string[] {
    if (count <= 0 || this.#points.length === 0) return [];
    const target = Math.min(count, this.#nodes.size);
    const start = this.#locate(hashKey(key));
    const picked = new Set<string>();
    const usedZones = new Set<string>();
    const overflow: string[] = [];
    for (let step = 0; step < this.#points.length && picked.size < target; step++) {
      const owner = this.#owners[(start + step) % this.#points.length];
      if (picked.has(owner)) continue;
      const zone = this.#nodes.get(owner)!.zone;
      if (usedZones.has(zone)) {
        overflow.push(owner);
        continue;
      }
      usedZones.add(zone);
      picked.add(owner);
    }
    for (const owner of overflow) {
      if (picked.size >= target) break;
      picked.add(owner);
    }
    return [...picked];
  }

  /**
   * Diff vnode ownership against another ring: every vnode point whose owner
   * differs represents a key range that would migrate on switchover.
   */
  diffOwnership(other: HashRing): OwnershipChange[] {
    const changes: OwnershipChange[] = [];
    for (let i = 0; i < this.#points.length; i++) {
      const point = this.#points[i];
      const before = this.#owners[i];
      const after = other.#owners[other.#locate(point)];
      if (before !== after) changes.push({ point, from: before, to: after });
    }
    return changes;
  }

  clone(): HashRing {
    const copy = new HashRing();
    for (const node of this.#nodes.values()) copy.#nodes.set(node.id, { ...node });
    copy.#points = [...this.#points];
    copy.#owners = [...this.#owners];
    copy.#version = this.#version;
    return copy;
  }

  /** Binary search: index of the first point >= hash, wrapping around. */
  #locate(hash: number): number {
    let lo = 0;
    let hi = this.#points.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.#points[mid] < hash) lo = mid + 1;
      else hi = mid;
    }
    return lo === this.#points.length ? 0 : lo;
  }

  #rebuild(): void {
    const entries: Array<[number, string]> = [];
    for (const node of this.#nodes.values()) {
      const replicas = node.weight * VNODES_PER_WEIGHT;
      for (let replica = 0; replica < replicas; replica++) {
        entries.push([hashVnode(node.id, replica), node.id]);
      }
    }
    entries.sort((a, b) => a[0] - b[0]);
    this.#points = entries.map(([point]) => point);
    this.#owners = entries.map(([, owner]) => owner);
    this.#version++;
  }
}
