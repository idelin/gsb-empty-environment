/**
 * Version history.
 *
 * Keeps a chain of surviving versions connected by structural edge mappings.
 * Queries between any two surviving versions compose edges (forward) or
 * their inverses (backward). Compression drops unpinned intermediate
 * versions and composes their edges, so queries between surviving versions
 * keep working unchanged. Undo/redo move the head along the chain; a new
 * edit after undo truncates the (unpinned) redo tail.
 */

import {
  applyEdit,
  makeVersion,
  type Edit,
  type IdAllocator,
  type Version,
  type VersionId,
} from './model.ts';
import {
  composeMappings,
  identityMapping,
  invertMapping,
  type Mapping,
} from './mapping.ts';

export class History {
  private alloc: IdAllocator = { next: 1 };
  private versions = new Map<VersionId, Version>();
  private chain: VersionId[] = [];
  private edges = new Map<VersionId, Mapping>(); // from-id -> edge to next in chain
  private pinned = new Set<VersionId>();
  private nextVid = 1;
  head: VersionId;

  constructor(text: string) {
    const v = makeVersion(this.nextVid++, text, this.alloc);
    this.versions.set(v.id, v);
    this.chain.push(v.id);
    this.head = v.id;
  }

  get liveVersionIds(): VersionId[] {
    return [...this.chain];
  }

  hasVersion(id: VersionId): boolean {
    return this.chain.includes(id);
  }

  getVersion(id: VersionId): Version {
    const v = this.versions.get(id);
    if (!v) throw new Error(`version ${id} was reclaimed`);
    return v;
  }

  textOf(id: VersionId): string {
    return this.getVersion(id).chars.map((c) => c.ch).join('');
  }

  pin(id: VersionId): void {
    this.getVersion(id);
    this.pinned.add(id);
  }

  unpin(id: VersionId): void {
    this.pinned.delete(id);
  }

  /** Apply an edit at the head, producing a new head version. */
  addVersion(edit: Edit): VersionId {
    const idx = this.chain.indexOf(this.head);
    // Truncate an unpinned redo tail (branching after undo).
    const tail = this.chain.splice(idx + 1);
    for (const id of tail) {
      if (this.pinned.has(id)) {
        this.chain.push(...tail);
        throw new Error(`cannot branch: redo tail contains pinned version ${id}`);
      }
      this.versions.delete(id);
    }
    const base = this.getVersion(this.head);
    const { version, mapping } = applyEdit(base, this.nextVid++, edit, this.alloc);
    this.versions.set(version.id, version);
    this.edges.set(this.head, mapping);
    this.chain.push(version.id);
    this.head = version.id;
    return version.id;
  }

  /** Structural mapping between any two surviving versions. */
  mappingBetween(from: VersionId, to: VersionId): Mapping {
    if (from === to) {
      return identityMapping(from, this.getVersion(from).chars.length);
    }
    const i = this.chain.indexOf(from);
    const j = this.chain.indexOf(to);
    if (i < 0) throw new Error(`version ${from} was reclaimed`);
    if (j < 0) throw new Error(`version ${to} was reclaimed`);
    if (i < j) {
      let acc = this.mustEdge(this.chain[i]);
      for (let k = i + 1; k < j; k++) acc = composeMappings(acc, this.mustEdge(this.chain[k]));
      return acc;
    }
    let acc: Mapping | null = null;
    for (let k = i - 1; k >= j; k--) {
      const inv = invertMapping(this.mustEdge(this.chain[k]));
      acc = acc ? composeMappings(acc, inv) : inv;
    }
    return acc as Mapping;
  }

  undo(): VersionId {
    const idx = this.chain.indexOf(this.head);
    if (idx === 0) throw new Error('nothing to undo');
    this.head = this.chain[idx - 1];
    return this.head;
  }

  redo(): VersionId {
    const idx = this.chain.indexOf(this.head);
    if (idx === this.chain.length - 1) throw new Error('nothing to redo');
    this.head = this.chain[idx + 1];
    return this.head;
  }

  /**
   * Reclaim unpinned intermediate versions. First version, head and pinned
   * versions always survive. Edges between survivors are composed, so
   * `mappingBetween` results for surviving pairs are unchanged.
   */
  compress(): VersionId[] {
    const keep = new Set<VersionId>([this.chain[0], this.head, ...this.pinned]);
    const newChain = this.chain.filter((id) => keep.has(id));
    const newEdges = new Map<VersionId, Mapping>();
    for (let k = 0; k + 1 < newChain.length; k++) {
      newEdges.set(newChain[k], this.mappingBetween(newChain[k], newChain[k + 1]));
    }
    const reclaimed: VersionId[] = [];
    for (const id of this.chain) {
      if (!keep.has(id)) {
        this.versions.delete(id);
        reclaimed.push(id);
      }
    }
    this.chain = newChain;
    this.edges = newEdges;
    return reclaimed;
  }

  private mustEdge(from: VersionId): Mapping {
    const m = this.edges.get(from);
    if (!m) throw new Error(`missing edge from version ${from}`);
    return m;
  }
}
