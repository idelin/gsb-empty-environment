/**
 * Version history: documents, per-version selection snapshots, and the
 * structural mappings between consecutive versions.
 *
 * - `commit` applies an edit and maps the live selections forward.
 * - `checkout` (branch undo) restores a version's exact selection snapshot,
 *   so selections that were merged away by a deletion come back with their
 *   original ids, directions and stickiness.
 * - `reclaim` + `compress` garbage-collect dead versions: mappings across
 *   reclaimed nodes are composed, so queries between surviving (live)
 *   versions keep working and return identical results.
 */

import { TextDocument, type NormalizedEdit } from './document.ts';
import { Mapping } from './mapping.ts';
import { SelectionSet, type MergeEvent } from './selections.ts';

interface VersionNode {
  readonly id: number;
  readonly doc: TextDocument;
  /** Exact selection snapshot; null once reclaimed. */
  selections: SelectionSet | null;
  live: boolean;
}

export interface CommitResult {
  readonly version: number;
  readonly mapping: Mapping;
  readonly edit: NormalizedEdit;
  readonly merges: MergeEvent[];
}

export class History {
  private nodes: VersionNode[] = [];
  /** edges[i] maps node i -> node i + 1. */
  private edges: Mapping[] = [];
  private current = 0;
  private nextVersionId = 0;

  /** Start a history with the initial document and selections. Returns version id. */
  init(doc: TextDocument, selections: SelectionSet): number {
    const id = this.nextVersionId++;
    this.nodes.push({ id, doc, selections, live: true });
    return id;
  }

  get currentVersion(): number {
    return this.nodes[this.current].id;
  }

  get size(): number {
    return this.nodes.length;
  }

  private indexOf(version: number): number {
    const i = this.nodes.findIndex((n) => n.id === version);
    if (i < 0) throw new Error(`unknown or reclaimed version ${version}`);
    return i;
  }

  docAt(version: number): TextDocument {
    return this.nodes[this.indexOf(version)].doc;
  }

  /** Selection snapshot of a live version (throws if the version was reclaimed). */
  selectionsAt(version: number): SelectionSet {
    const node = this.nodes[this.indexOf(version)];
    if (node.selections === null) throw new Error(`version ${version} was reclaimed`);
    return node.selections;
  }

  /** Structural mapping between any two surviving versions (either direction). */
  mappingBetween(from: number, to: number): Mapping {
    const a = this.indexOf(from);
    const b = this.indexOf(to);
    if (a === b) return Mapping.identity();
    if (a < b) return Mapping.compose(...this.edges.slice(a, b));
    return Mapping.compose(...this.edges.slice(b, a)).invert();
  }

  /** Apply an edit on top of the current version and map selections forward. */
  commit(start: number, deleteLen: number, insert: string): CommitResult {
    const node = this.nodes[this.current];
    if (node.selections === null) throw new Error('current version was reclaimed');
    // Branching: drop any redo tail after the current version.
    this.nodes.length = this.current + 1;
    this.edges.length = this.current;

    const { doc, mapping, edit } = node.doc.applyEdit(start, deleteLen, insert);
    const { set, merges } = node.selections.mapForward(mapping);
    const id = this.nextVersionId++;
    this.nodes.push({ id, doc, selections: set, live: true });
    this.edges.push(mapping);
    this.current = this.nodes.length - 1;
    return { version: id, mapping, edit, merges };
  }

  /**
   * Branch undo: move the current pointer to an earlier surviving version and
   * return its exact selection snapshot (merged selections are restored).
   */
  checkout(version: number): SelectionSet {
    const i = this.indexOf(version);
    const node = this.nodes[i];
    if (node.selections === null) throw new Error(`version ${version} was reclaimed`);
    this.current = i;
    return node.selections;
  }

  /** Mark a version dead so `compress` can fold it out of the chain. */
  reclaim(version: number): void {
    const i = this.indexOf(version);
    if (i === this.current) throw new Error('cannot reclaim the current version');
    const node = this.nodes[i];
    node.selections = null;
    node.live = false;
  }

  /**
   * Compose mappings across reclaimed versions and drop them. Live versions
   * keep their snapshots; mappings between any two survivors are unchanged.
   */
  compress(): void {
    const currentId = this.nodes[this.current].id;
    const keptNodes: VersionNode[] = [];
    const keptEdges: Mapping[] = [];
    let pending = Mapping.identity();

    for (let i = 0; i < this.nodes.length; i++) {
      const node = this.nodes[i];
      if (node.live) {
        keptNodes.push(node);
        if (keptNodes.length > 1) keptEdges.push(pending);
        pending = Mapping.identity();
      }
      if (i < this.edges.length) pending = pending.compose(this.edges[i]);
    }
    this.nodes = keptNodes;
    this.edges = keptEdges;
    this.current = this.nodes.findIndex((n) => n.id === currentId);
  }
}
