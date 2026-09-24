import {
  applyEditToText,
  transformRange,
  transformRangeBackward,
} from './edits.ts';
import { captureContext } from './diagnostic.ts';
import { similarityRelocate } from './similarity.ts';
import type {
  Diagnostic,
  Edit,
  Evidence,
  Range,
  Relocation,
} from './types.ts';

type MarkerState = {
  status: 'alive' | 'deleted';
  range: Range | null;
  /** Accumulated transform evidence since the marker was created. */
  trail: Evidence[];
};

type VersionNode = {
  id: number;
  parent: number | null;
  label: string;
  /** Edits that produced this version from its parent. */
  edits: Edit[];
  text: string;
  markers: Map<string, MarkerState>;
};

export type MapResult =
  | { status: 'mapped'; range: Range }
  | { status: 'deleted' }
  | { status: 'unknown'; reason: string };

let diagnosticCounter = 0;

/**
 * A version DAG with per-version marker snapshots.
 *
 * Identity tracking: a diagnostic owns a marker with a stable id. Every
 * commit transforms all markers through the commit's edits and records
 * evidence. A marker whose range is fully deleted becomes a tombstone:
 * the diagnostic is invalid from that version on, and identical text
 * appearing elsewhere never revives it. Only a `move` edit carrying a
 * stable `moveId` transports markers precisely.
 */
export class History {
  private versions = new Map<number, VersionNode>();
  private pins = new Set<number>();
  private nextVersionId = 0;
  private nextMarkerId = 0;
  private headId: number;

  constructor(initialText: string) {
    const root: VersionNode = {
      id: this.nextVersionId++,
      parent: null,
      label: 'root',
      edits: [],
      text: initialText,
      markers: new Map(),
    };
    this.versions.set(root.id, root);
    this.headId = root.id;
  }

  get head(): number {
    return this.headId;
  }

  has(versionId: number): boolean {
    return this.versions.has(versionId);
  }

  text(versionId: number = this.headId): string {
    return this.node(versionId).text;
  }

  private node(id: number): VersionNode {
    const n = this.versions.get(id);
    if (!n) throw new RangeError(`unknown or reclaimed version ${id}`);
    return n;
  }

  /** Apply edits as a new version on top of head. Returns the version id. */
  commit(edits: Edit[], label = 'commit'): number {
    const parent = this.node(this.headId);
    const id = this.nextVersionId++;
    let text = parent.text;
    const markers = new Map<string, MarkerState>();
    for (const [markerId, m] of parent.markers) {
      markers.set(markerId, { ...m });
    }
    edits.forEach((edit, editIndex) => {
      for (const [markerId, m] of markers) {
        if (m.status !== 'alive' || m.range === null) continue;
        const t = transformRange(m.range, edit);
        if (t.status === 'deleted') {
          markers.set(markerId, {
            status: 'deleted',
            range: null,
            trail: [
              ...m.trail,
              { version: { from: parent.id, to: id }, editIndex, kind: 'deleted', detail: t.detail },
            ],
          });
        } else if (t.kind !== 'unchanged') {
          markers.set(markerId, {
            status: 'alive',
            range: t.range,
            trail: [
              ...m.trail,
              { version: { from: parent.id, to: id }, editIndex, kind: t.kind, detail: t.detail },
            ],
          });
        }
      }
      text = applyEditToText(text, edit);
    });
    this.versions.set(id, { id, parent: parent.id, label, edits, text, markers });
    this.headId = id;
    return id;
  }

  /** Move head to its parent, keeping the undone version for redo/branching. */
  undo(): number {
    const parent = this.node(this.headId).parent;
    if (parent === null) throw new RangeError('nothing to undo');
    this.headId = parent;
    return parent;
  }

  checkout(versionId: number): void {
    this.node(versionId);
    this.headId = versionId;
  }

  /** Lowest common ancestor of two retained versions. */
  lca(a: number, b: number): number | null {
    const ancestors = new Set<number>();
    for (let n: number | null = a; n !== null; n = this.node(n).parent) {
      ancestors.add(n);
    }
    for (let n: number | null = b; n !== null; n = this.node(n).parent) {
      if (ancestors.has(n)) return n;
    }
    return null;
  }

  /**
   * Rebase-style merge: replay the edits of `sourceId`'s branch (since the
   * common ancestor) onto head as one new version. Markers follow through
   * the replayed edits, so a stable-id move on the merged branch is
   * tracked precisely. Offset conflicts are the caller's responsibility.
   */
  merge(sourceId: number): number {
    const base = this.lca(this.headId, sourceId);
    if (base === null) throw new RangeError('versions share no history');
    const edits: Edit[] = [];
    for (let n = sourceId; n !== base; n = this.node(n).parent!) {
      edits.unshift(...this.node(n).edits);
    }
    return this.commit(edits, `merge of ${sourceId}`);
  }

  pin(versionId: number): void {
    this.node(versionId);
    this.pins.add(versionId);
  }

  unpin(versionId: number): void {
    this.pins.delete(versionId);
  }

  /**
   * Reclaim every version that is not head, pinned, or an ancestor of one
   * of those. Diagnostics bound to reclaimed versions lose their identity
   * chain and fall back to context-based text similarity.
   */
  reclaim(): number[] {
    const keep = new Set<number>();
    const keepWithAncestors = (id: number): void => {
      for (let n: number | null = id; n !== null && !keep.has(n); n = this.node(n).parent) {
        keep.add(n);
      }
    };
    keepWithAncestors(this.headId);
    for (const p of this.pins) keepWithAncestors(p);
    const removed: number[] = [];
    for (const id of this.versions.keys()) {
      if (!keep.has(id)) {
        this.versions.delete(id);
        removed.push(id);
      }
    }
    return removed;
  }

  /** Bind a diagnostic to a UTF-16 range at the current head version. */
  bind(options: { range: Range; message?: string; contextRadius?: number }): Diagnostic {
    const node = this.node(this.headId);
    const { range } = options;
    if (range.start < 0 || range.end > node.text.length || range.start >= range.end) {
      throw new RangeError(`invalid diagnostic range [${range.start}, ${range.end})`);
    }
    const markerId = `m${this.nextMarkerId++}`;
    node.markers.set(markerId, { status: 'alive', range, trail: [] });
    diagnosticCounter++;
    return {
      id: `d${diagnosticCounter}`,
      markerId,
      bindVersion: node.id,
      range,
      context: captureContext(node.text, range, options.contextRadius),
      message: options.message ?? '',
    };
  }

  /**
   * Relocate a diagnostic to `targetId` (default: head).
   *
   * Resolution order:
   *  1. Identity: the marker snapshot at the target version. Alive ->
   *     certain; tombstone -> invalid (never revived by identical text).
   *  2. Text similarity fallback with explicit candidate/exclusion
   *     evidence. Duplicates produce `ambiguous`, never a silent pick.
   */
  relocate(diagnostic: Diagnostic, targetId: number = this.headId): Relocation {
    const target = this.versions.get(targetId);
    if (!target) {
      return {
        status: 'invalid',
        reason: `target version ${targetId} was reclaimed or never existed`,
        evidence: ['history reclamation removed the target version'],
      };
    }
    const marker = target.markers.get(diagnostic.markerId);
    if (marker) {
      if (marker.status === 'alive' && marker.range) {
        return {
          status: 'certain',
          basis: 'identity',
          range: marker.range,
          evidence: marker.trail,
          excluded: [],
        };
      }
      return {
        status: 'invalid',
        reason: 'diagnostic target was deleted; identical text elsewhere does not revive it',
        evidence: marker.trail,
      };
    }
    const bindKnown = this.versions.has(diagnostic.bindVersion);
    const note = bindKnown
      ? `marker ${diagnostic.markerId} does not exist at version ${targetId} (different branch); falling back to text similarity`
      : `bind version ${diagnostic.bindVersion} was reclaimed; falling back to text similarity`;
    return similarityRelocate(diagnostic.context, target.text, note);
  }

  /**
   * Compose a position mapping for an arbitrary range between two retained
   * versions, walking the version path (down to the LCA, then up) and
   * composing each step's per-edit transform.
   */
  mapRange(range: Range, fromId: number, toId: number): MapResult {
    if (!this.versions.has(fromId) || !this.versions.has(toId)) {
      return { status: 'unknown', reason: 'endpoint version was reclaimed' };
    }
    const base = this.lca(fromId, toId);
    if (base === null) return { status: 'unknown', reason: 'versions share no history' };

    // Downward: fromId -> base, applying inverse edits in reverse order.
    const down: VersionNode[] = [];
    for (let n = fromId; n !== base; n = this.node(n).parent!) {
      down.push(this.node(n));
    }
    let current: Range | null = range;
    for (const node of down) {
      for (let i = node.edits.length - 1; i >= 0 && current; i--) {
        const t = transformRangeBackward(current, node.edits[i]);
        if (t.status === 'deleted') return { status: 'deleted' };
        current = t.range;
      }
    }
    // Upward: base -> toId, applying edits forward.
    const up: VersionNode[] = [];
    for (let n = toId; n !== base; n = this.node(n).parent!) {
      up.push(this.node(n));
    }
    up.reverse();
    for (const node of up) {
      for (const edit of node.edits) {
        if (!current) break;
        const t = transformRange(current, edit);
        if (t.status === 'deleted') return { status: 'deleted' };
        current = t.range;
      }
    }
    return current ? { status: 'mapped', range: current } : { status: 'deleted' };
  }
}
