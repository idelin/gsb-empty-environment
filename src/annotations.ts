export type Stickiness = 'left' | 'right';

export interface Annotation {
  id: number;
  start: number;
  end: number;
  startSticky: Stickiness;
  endSticky: Stickiness;
  active: boolean;
}

export type Edit =
  | { kind: 'insert'; pos: number; text: string }
  | { kind: 'delete'; start: number; end: number }
  | { kind: 'replace'; start: number; end: number; text: string };

export interface Segment {
  start: number;
  end: number;
  ids: number[];
}

interface Snapshot {
  text: string;
  prior: Map<number, Annotation>;
}

export function normalizeEdits(edits: Edit[]): Edit[] {
  const out: Edit[] = [];
  for (const edit of edits) {
    if (edit.kind === 'insert') {
      if (edit.text.length > 0) out.push(edit);
    } else if (edit.kind === 'delete') {
      if (edit.start < edit.end) out.push(edit);
    } else {
      if (edit.start < edit.end) {
        out.push({ kind: 'delete', start: edit.start, end: edit.end });
      }
      if (edit.text.length > 0) {
        out.push({ kind: 'insert', pos: edit.start, text: edit.text });
      }
    }
  }
  return out;
}

function mapInsert(pos: number, sticky: Stickiness, at: number, len: number): number {
  if (pos < at) return pos;
  if (pos > at) return pos + len;
  return sticky === 'left' ? pos : pos + len;
}

function mapDelete(pos: number, start: number, end: number): number {
  if (pos <= start) return pos;
  if (pos >= end) return pos - (end - start);
  return start;
}

function sameIds(a: number[], b: number[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

export class AnnotationStore {
  private _text: string;
  private anns = new Map<number, Annotation>();
  private nextId = 1;
  private history: Snapshot[] = [];

  constructor(text = '') {
    this._text = text;
  }

  get text(): string {
    return this._text;
  }

  get length(): number {
    return this._text.length;
  }

  get canUndo(): boolean {
    return this.history.length > 0;
  }

  addAnnotation(
    start: number,
    end: number,
    startSticky: Stickiness = 'right',
    endSticky: Stickiness = 'left',
  ): number {
    if (!(0 <= start && start <= end && end <= this._text.length)) {
      throw new RangeError(`invalid annotation range [${start}, ${end}) for length ${this._text.length}`);
    }
    if (start === end) endSticky = startSticky;
    const id = this.nextId++;
    this.anns.set(id, { id, start, end, startSticky, endSticky, active: true });
    return id;
  }

  removeAnnotation(id: number): boolean {
    return this.anns.delete(id);
  }

  get(id: number): Annotation | undefined {
    const ann = this.anns.get(id);
    return ann ? { ...ann } : undefined;
  }

  annotations(): Annotation[] {
    return [...this.anns.values()]
      .map((a) => ({ ...a }))
      .sort((x, y) => x.id - y.id);
  }

  activeAnnotations(): Annotation[] {
    return this.annotations().filter((a) => a.active);
  }

  invalidIds(): number[] {
    return this.annotations()
      .filter((a) => !a.active)
      .map((a) => a.id);
  }

  applyEdit(edit: Edit): void {
    this.applyEdits([edit]);
  }

  applyEdits(edits: Edit[]): void {
    const normalized = normalizeEdits(edits);
    if (normalized.length === 0) return;
    const prior = new Map<number, Annotation>();
    for (const [id, ann] of this.anns) prior.set(id, { ...ann });
    this.history.push({ text: this._text, prior });
    for (const edit of normalized) {
      if (edit.kind === 'insert') this.insertText(edit.pos, edit.text);
      else this.deleteRange(edit.start, edit.end);
    }
  }

  undo(): boolean {
    const snap = this.history.pop();
    if (!snap) return false;
    this._text = snap.text;
    this.anns = new Map<number, Annotation>();
    for (const [id, ann] of snap.prior) this.anns.set(id, { ...ann });
    return true;
  }

  query(start: number, end: number): Annotation[] {
    if (!(0 <= start && start <= end && end <= this._text.length)) {
      throw new RangeError(`invalid query range [${start}, ${end}) for length ${this._text.length}`);
    }
    const pointQuery = start === end;
    return this.activeAnnotations().filter((a) => {
      if (a.start === a.end) {
        return pointQuery ? a.start === start : start <= a.start && a.start < end;
      }
      if (pointQuery) return a.start <= start && start < a.end;
      return a.start < end && start < a.end;
    });
  }

  segments(): Segment[] {
    const active = this.activeAnnotations();
    const bounds = new Set<number>();
    for (const a of active) {
      bounds.add(a.start);
      bounds.add(a.end);
    }
    const pts = [...bounds].sort((x, y) => x - y);
    const segments: Segment[] = [];
    for (let i = 0; i + 1 < pts.length; i++) {
      const s = pts[i];
      const e = pts[i + 1];
      if (s === e) continue;
      const ids = active
        .filter((a) => a.start < a.end && a.start <= s && a.end >= e)
        .map((a) => a.id)
        .sort((x, y) => x - y);
      if (ids.length === 0) continue;
      const last = segments[segments.length - 1];
      if (last && last.end === s && sameIds(last.ids, ids)) {
        last.end = e;
      } else {
        segments.push({ start: s, end: e, ids });
      }
    }
    const pointIds = new Map<number, number[]>();
    for (const a of active) {
      if (a.start !== a.end) continue;
      const list = pointIds.get(a.start);
      if (list) list.push(a.id);
      else pointIds.set(a.start, [a.id]);
    }
    for (const [pos, ids] of pointIds) {
      ids.sort((x, y) => x - y);
      segments.push({ start: pos, end: pos, ids });
    }
    segments.sort((x, y) => x.start - y.start || x.end - y.end);
    return segments;
  }

  private insertText(pos: number, text: string): void {
    if (!(0 <= pos && pos <= this._text.length)) {
      throw new RangeError(`invalid insert position ${pos} for length ${this._text.length}`);
    }
    this._text = this._text.slice(0, pos) + text + this._text.slice(pos);
    const len = text.length;
    for (const a of this.anns.values()) {
      if (!a.active) continue;
      a.start = mapInsert(a.start, a.startSticky, pos, len);
      a.end = mapInsert(a.end, a.endSticky, pos, len);
    }
  }

  private deleteRange(start: number, end: number): void {
    if (!(0 <= start && start < end && end <= this._text.length)) {
      throw new RangeError(`invalid delete range [${start}, ${end}) for length ${this._text.length}`);
    }
    this._text = this._text.slice(0, start) + this._text.slice(end);
    for (const a of this.anns.values()) {
      if (!a.active) continue;
      const zeroWidth = a.start === a.end;
      const invalidated = zeroWidth
        ? start < a.start && a.start < end
        : start <= a.start && a.end <= end;
      if (invalidated) {
        a.active = false;
        a.start = start;
        a.end = start;
      } else {
        a.start = mapDelete(a.start, start, end);
        a.end = mapDelete(a.end, start, end);
      }
    }
  }
}
