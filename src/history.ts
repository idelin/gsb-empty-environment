import { applyEdit, type Edit } from './edits.ts';
import {
  composeMappings,
  identityMapping,
  invertMapping,
  mapOffset,
  mappingFromEdit,
  type SegmentMapping,
} from './mapping.ts';

export interface Version {
  id: number;
  seq: number;
  parent: number | null;
  edit: Edit | null;
  mapping: SegmentMapping | null;
  text: string;
  synthetic: boolean;
}

export interface MappingStep {
  version: Version;
  direction: 'forward' | 'undo';
}

export interface MappingPath {
  mapping: SegmentMapping;
  steps: MappingStep[];
}

export class History {
  private versions = new Map<number, Version>();
  private nextId = 1;
  private seqCounter = 0;
  private editCounter = 0;
  current: number;

  constructor(initialText: string) {
    const root: Version = {
      id: 0,
      seq: 0,
      parent: null,
      edit: null,
      mapping: null,
      text: initialText,
      synthetic: false,
    };
    this.versions.set(0, root);
    this.current = 0;
  }

  has(version: number): boolean {
    return this.versions.has(version);
  }

  get(version: number): Version {
    const v = this.versions.get(version);
    if (!v) throw new Error('version v' + version + ' does not exist (reclaimed or never created)');
    return v;
  }

  text(version = this.current): string {
    return this.get(version).text;
  }

  nextEditId(): number {
    return ++this.editCounter;
  }

  applyEdit(edit: Edit): Version {
    const parent = this.get(this.current);
    const text = applyEdit(parent.text, edit);
    const mapping = mappingFromEdit(parent.text.length, edit);
    const version: Version = {
      id: this.nextId++,
      seq: ++this.seqCounter,
      parent: parent.id,
      edit,
      mapping,
      text,
      synthetic: false,
    };
    this.versions.set(version.id, version);
    this.current = version.id;
    return version;
  }

  undo(): boolean {
    const v = this.get(this.current);
    if (v.parent === null) return false;
    this.current = v.parent;
    return true;
  }

  checkout(version: number): void {
    this.get(version);
    this.current = version;
  }

  mappingBetween(from: number, to: number): MappingPath | null {
    const fromV = this.versions.get(from);
    const toV = this.versions.get(to);
    if (!fromV || !toV) return null;
    const ancestors = new Set<number>();
    let cur: Version | undefined = fromV;
    while (cur) {
      ancestors.add(cur.id);
      cur = cur.parent === null ? undefined : this.versions.get(cur.parent);
    }
    const downPath: Version[] = [];
    cur = toV;
    while (!ancestors.has(cur.id)) {
      downPath.push(cur);
      if (cur.parent === null) return null;
      cur = this.versions.get(cur.parent);
      if (!cur) return null;
    }
    const lca = cur;
    let mapping = identityMapping(fromV.text.length);
    const steps: MappingStep[] = [];
    cur = fromV;
    while (cur.id !== lca.id) {
      mapping = composeMappings(mapping, invertMapping(cur.mapping!));
      steps.push({ version: cur, direction: 'undo' });
      cur = this.versions.get(cur.parent!);
      if (!cur) return null;
    }
    for (let i = downPath.length - 1; i >= 0; i--) {
      mapping = composeMappings(mapping, downPath[i]!.mapping!);
      steps.push({ version: downPath[i]!, direction: 'forward' });
    }
    return { mapping, steps };
  }

  merge(branchTip: number): Version {
    const tip = this.get(branchTip);
    const path = this.mappingBetween(this.current, tip.id);
    if (!path) throw new Error('no path between current version and branch tip');
    const edits: Edit[] = [];
    let cursor = this.current;
    for (const step of path.steps) {
      if (step.direction !== 'forward' || !step.version.edit) continue;
      const translated = this.translateEdit(step.version.edit, step.version.parent!, cursor);
      edits.push(translated);
      cursor = this.applyEdit(translated).id;
    }
    if (edits.length === 0) return this.get(this.current);
    return this.get(this.current);
  }

  private translateEdit(edit: Edit, authoredAt: number, targetVersion: number): Edit {
    const path = this.mappingBetween(authoredAt, targetVersion);
    if (!path) throw new Error('merge conflict: no mapping path for edit#' + edit.id);
    const mapPoint = (offset: number, what: string): number => {
      const mapped = mapOffset(path.mapping, offset, 'right');
      if (mapped === null) {
        throw new Error('merge conflict: ' + what + ' of edit#' + edit.id + ' was deleted');
      }
      return mapped;
    };
    const id = this.nextEditId();
    switch (edit.kind) {
      case 'insert':
        return { kind: 'insert', id, offset: mapPoint(edit.offset, 'offset'), text: edit.text };
      case 'delete':
        return {
          kind: 'delete',
          id,
          start: mapPoint(edit.start, 'start'),
          end: mapPoint(edit.end, 'end'),
        };
      case 'move':
        return {
          kind: 'move',
          id,
          start: mapPoint(edit.start, 'start'),
          end: mapPoint(edit.end, 'end'),
          to: mapPoint(edit.to, 'target'),
          stableId: edit.stableId,
        };
    }
  }

  reclaim(
    keepLast: number,
    pinned: ReadonlySet<number> = new Set(),
  ): { reclaimed: number[]; checkpoint: number | null } {
    const chain: Version[] = [];
    let cur: Version | undefined = this.get(this.current);
    while (cur) {
      chain.unshift(cur);
      cur = cur.parent === null ? undefined : this.versions.get(cur.parent);
    }
    const retained = new Set<number>();
    retained.add(this.current);
    for (let i = Math.max(0, chain.length - keepLast); i < chain.length; i++) {
      retained.add(chain[i]!.id);
    }
    for (const v of chain) {
      if (pinned.has(v.id)) retained.add(v.id);
    }
    const reclaimed: number[] = [];
    let checkpointId: number | null = null;
    let prevRetained: Version | null = null;
    let i = 0;
    while (i < chain.length) {
      const v = chain[i]!;
      if (retained.has(v.id)) {
        prevRetained = v;
        i++;
        continue;
      }
      let j = i;
      while (j < chain.length && !retained.has(chain[j]!.id)) j++;
      const dropped = chain.slice(i, j);
      const lastDropped = dropped[dropped.length - 1]!;
      let squashed = prevRetained ? identityMapping(prevRetained.text.length) : null;
      for (const d of dropped) {
        squashed = squashed ? composeMappings(squashed, d.mapping!) : null;
      }
      const checkpoint: Version = {
        id: this.nextId++,
        seq: lastDropped.seq,
        parent: prevRetained ? prevRetained.id : null,
        edit: null,
        mapping: squashed,
        text: lastDropped.text,
        synthetic: true,
      };
      this.versions.set(checkpoint.id, checkpoint);
      checkpointId = checkpoint.id;
      const next = j < chain.length ? chain[j]! : null;
      if (next) next.parent = checkpoint.id;
      for (const d of dropped) {
        this.versions.delete(d.id);
        reclaimed.push(d.id);
      }
      prevRetained = checkpoint;
      i = j;
    }
    let changed = true;
    while (changed) {
      changed = false;
      for (const v of [...this.versions.values()]) {
        if (v.parent !== null && !this.versions.has(v.parent) && !pinned.has(v.id)) {
          this.versions.delete(v.id);
          reclaimed.push(v.id);
          changed = true;
        }
      }
    }
    return { reclaimed, checkpoint: checkpointId };
  }
}
