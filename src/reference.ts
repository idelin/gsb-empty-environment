import { Edit, Stickiness, normalizeEdits } from './annotations';

interface RefChar {
  kind: 'char';
  uid: number;
  ch: string;
}

interface RefGap {
  kind: 'gap';
  uid: number;
}

type RefItem = RefChar | RefGap;

export interface RefRangeAnn {
  kind: 'range';
  id: number;
  startSticky: Stickiness;
  endSticky: Stickiness;
  cover: Set<number>;
  active: boolean;
}

export interface RefPointAnn {
  kind: 'point';
  id: number;
  sticky: Stickiness;
  gapUid: number;
  active: boolean;
}

export type RefAnn = RefRangeAnn | RefPointAnn;

export interface RefAnnDebug {
  id: number;
  kind: 'range' | 'point';
  active: boolean;
  covered: number[];
  gapPosition: number | null;
}

interface RefSnapshot {
  items: RefItem[];
  nextUid: number;
  anns: Map<number, RefAnn>;
}

function cloneAnn(a: RefAnn): RefAnn {
  if (a.kind === 'range') return { ...a, cover: new Set(a.cover) };
  return { ...a };
}

export class ReferenceModel {
  private items: RefItem[] = [];
  private nextUid = 0;
  private anns = new Map<number, RefAnn>();
  private nextId = 1;
  private history: RefSnapshot[] = [];

  constructor(text = '') {
    this.items.push({ kind: 'gap', uid: this.nextUid++ });
    for (const ch of text) {
      this.items.push({ kind: 'char', uid: this.nextUid++, ch });
      this.items.push({ kind: 'gap', uid: this.nextUid++ });
    }
  }

  get canUndo(): boolean {
    return this.history.length > 0;
  }

  text(): string {
    return this.items
      .filter((it): it is RefChar => it.kind === 'char')
      .map((it) => it.ch)
      .join('');
  }

  addAnnotation(start: number, end: number, startSticky: Stickiness, endSticky: Stickiness): number {
    const id = this.nextId++;
    if (start === end) {
      const gapUid = this.gapUids()[start];
      this.anns.set(id, { kind: 'point', id, sticky: startSticky, gapUid, active: true });
    } else {
      const cover = new Set(this.charUids().slice(start, end));
      this.anns.set(id, { kind: 'range', id, startSticky, endSticky, cover, active: true });
    }
    return id;
  }

  removeAnnotation(id: number): boolean {
    return this.anns.delete(id);
  }

  applyEdit(edit: Edit): void {
    this.applyEdits([edit]);
  }

  applyEdits(edits: Edit[]): void {
    const normalized = normalizeEdits(edits);
    if (normalized.length === 0) return;
    const prior = new Map<number, RefAnn>();
    for (const [id, ann] of this.anns) prior.set(id, cloneAnn(ann));
    this.history.push({ items: [...this.items], nextUid: this.nextUid, anns: prior });
    for (const edit of normalized) {
      if (edit.kind === 'insert') this.insertText(edit.pos, edit.text);
      else this.deleteRange(edit.start, edit.end);
    }
  }

  undo(): boolean {
    const snap = this.history.pop();
    if (!snap) return false;
    this.items = snap.items;
    this.nextUid = snap.nextUid;
    this.anns = snap.anns;
    return true;
  }

  charCoverage(): number[][] {
    const chars = this.charUids();
    const indexOf = new Map<number, number>();
    chars.forEach((uid, i) => indexOf.set(uid, i));
    const result: number[][] = chars.map(() => []);
    for (const a of this.anns.values()) {
      if (!a.active || a.kind !== 'range') continue;
      for (const uid of a.cover) {
        const i = indexOf.get(uid);
        if (i !== undefined) result[i].push(a.id);
      }
    }
    for (const list of result) list.sort((x, y) => x - y);
    return result;
  }

  gapCoverage(): number[][] {
    const gaps = this.gapUids();
    const indexOf = new Map<number, number>();
    gaps.forEach((uid, i) => indexOf.set(uid, i));
    const result: number[][] = gaps.map(() => []);
    for (const a of this.anns.values()) {
      if (!a.active || a.kind !== 'point') continue;
      const i = indexOf.get(a.gapUid);
      if (i !== undefined) result[i].push(a.id);
    }
    for (const list of result) list.sort((x, y) => x - y);
    return result;
  }

  invalidIds(): number[] {
    return [...this.anns.values()]
      .filter((a) => !a.active)
      .map((a) => a.id)
      .sort((x, y) => x - y);
  }

  debugAnnotations(): RefAnnDebug[] {
    const charIndex = new Map<number, number>();
    this.charUids().forEach((uid, i) => charIndex.set(uid, i));
    const gapIndex = new Map<number, number>();
    this.gapUids().forEach((uid, i) => gapIndex.set(uid, i));
    return [...this.anns.values()].map((a) => {
      if (a.kind === 'range') {
        const covered = [...a.cover]
          .map((uid) => charIndex.get(uid))
          .filter((i): i is number => i !== undefined)
          .sort((x, y) => x - y);
        return { id: a.id, kind: 'range', active: a.active, covered, gapPosition: null };
      }
      const pos = gapIndex.get(a.gapUid);
      return { id: a.id, kind: 'point', active: a.active, covered: [], gapPosition: pos === undefined ? null : pos };
    });
  }

  private charUids(): number[] {
    const uids: number[] = [];
    for (const it of this.items) if (it.kind === 'char') uids.push(it.uid);
    return uids;
  }

  private gapUids(): number[] {
    const uids: number[] = [];
    for (const it of this.items) if (it.kind === 'gap') uids.push(it.uid);
    return uids;
  }

  private insertText(pos: number, text: string): void {
    const targetGapUid = this.gapUids()[pos];
    const idx = this.items.findIndex((it) => it.kind === 'gap' && it.uid === targetGapUid);
    const charsBefore = this.charUids();
    const indexOf = new Map<number, number>();
    charsBefore.forEach((uid, i) => indexOf.set(uid, i));
    const expands = new Set<number>();
    for (const a of this.anns.values()) {
      if (!a.active || a.kind !== 'range' || a.cover.size === 0) continue;
      let first = Infinity;
      let last = -Infinity;
      for (const uid of a.cover) {
        const i = indexOf.get(uid);
        if (i === undefined) continue;
        if (i < first) first = i;
        if (i > last) last = i;
      }
      const covers =
        (first < pos && pos < last + 1) ||
        (pos === first && a.startSticky === 'left') ||
        (pos === last + 1 && a.endSticky === 'right');
      if (covers) expands.add(a.id);
    }
    const inserted: RefItem[] = [];
    const insertedCharUids: number[] = [];
    let lastGapUid = targetGapUid;
    for (const ch of text) {
      const charItem: RefChar = { kind: 'char', uid: this.nextUid++, ch };
      const gapItem: RefGap = { kind: 'gap', uid: this.nextUid++ };
      inserted.push(charItem, gapItem);
      insertedCharUids.push(charItem.uid);
      lastGapUid = gapItem.uid;
    }
    this.items.splice(idx + 1, 0, ...inserted);
    for (const a of this.anns.values()) {
      if (!a.active) continue;
      if (a.kind === 'point' && a.gapUid === targetGapUid && a.sticky === 'right') {
        a.gapUid = lastGapUid;
      }
      if (a.kind === 'range' && expands.has(a.id)) {
        for (const uid of insertedCharUids) a.cover.add(uid);
      }
    }
  }

  private deleteRange(start: number, end: number): void {
    const gaps = this.gapUids();
    const surviveGapUid = gaps[start];
    const mergeGapUid = gaps[end];
    const removedCharUids = new Set(this.charUids().slice(start, end));
    const interiorGapUids = new Set(gaps.slice(start + 1, end));
    const dropGapUids = new Set(interiorGapUids);
    dropGapUids.add(mergeGapUid);
    this.items = this.items.filter((it) =>
      it.kind === 'char' ? !removedCharUids.has(it.uid) : !dropGapUids.has(it.uid),
    );
    for (const a of this.anns.values()) {
      if (!a.active) continue;
      if (a.kind === 'range') {
        for (const uid of removedCharUids) a.cover.delete(uid);
        if (a.cover.size === 0) a.active = false;
      } else if (interiorGapUids.has(a.gapUid)) {
        a.active = false;
      } else if (a.gapUid === mergeGapUid) {
        a.gapUid = surviveGapUid;
      }
    }
  }
}
