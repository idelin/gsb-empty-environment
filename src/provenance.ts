export type ReaderId = string;
export type SourceId = string;

export interface Redaction {
  start: number;
  end: number;
  reasons: SourceId[];
}

export interface VisibilityReport {
  text: string;
  redactions: Redaction[];
}

export const REDACTION_CHAR = '█';

export class SourceRegistry {
  private readonly sources = new Map<SourceId, ReadonlySet<ReaderId>>();

  register(id: SourceId, readers: Iterable<ReaderId>): void {
    this.sources.set(id, new Set(readers));
  }

  has(id: SourceId): boolean {
    return this.sources.has(id);
  }

  readersOf(id: SourceId): ReadonlySet<ReaderId> {
    const readers = this.sources.get(id);
    if (!readers) throw new Error(`unknown source: ${id}`);
    return readers;
  }

  isVisible(cellSources: ReadonlySet<SourceId>, reader: ReaderId): boolean {
    for (const source of cellSources) {
      if (!this.readersOf(source).has(reader)) return false;
    }
    return true;
  }

  blockingSources(cellSources: ReadonlySet<SourceId>, reader: ReaderId): SourceId[] {
    const blocking: SourceId[] = [];
    for (const source of cellSources) {
      if (!this.readersOf(source).has(reader)) blocking.push(source);
    }
    return blocking.sort();
  }
}

interface Span {
  length: number;
  sources: ReadonlySet<SourceId>;
}

function spanKey(sources: ReadonlySet<SourceId>): string {
  return JSON.stringify([...sources].sort());
}

function cloneSpans(spans: Span[]): Span[] {
  return spans.map((span) => ({ length: span.length, sources: span.sources }));
}

export class Document {
  private text = '';
  private spans: Span[] = [];

  readonly registry: SourceRegistry;

  constructor(registry: SourceRegistry) {
    this.registry = registry;
  }

  static fromFragment(registry: SourceRegistry, source: SourceId, text: string): Document {
    const doc = new Document(registry);
    doc.appendFragment(source, text);
    return doc;
  }

  get length(): number {
    return this.text.length;
  }

  toString(): string {
    return this.text;
  }

  appendFragment(source: SourceId, text: string): void {
    this.insertFragment(this.length, source, text);
  }

  insertFragment(pos: number, source: SourceId, text: string): void {
    this.registry.readersOf(source);
    this.splice(pos, 0, text, [{ length: text.length, sources: new Set([source]) }]);
  }

  insertLiteral(pos: number, text: string): void {
    this.splice(pos, 0, text, [{ length: text.length, sources: new Set<SourceId>() }]);
  }

  insertDocument(pos: number, other: Document): void {
    this.assertSameRegistry(other);
    this.splice(pos, 0, other.text, cloneSpans(other.spans));
  }

  copyRange(start: number, end: number, dest: number): void {
    const piece = this.extract(start, end);
    this.splice(dest, 0, piece.text, piece.spans);
  }

  moveRange(start: number, end: number, dest: number): void {
    const piece = this.extract(start, end);
    const length = end - start;
    this.splice(start, length, '', []);
    let adjusted = dest;
    if (dest > start) adjusted = dest >= end ? dest - length : start;
    adjusted = Math.max(0, Math.min(adjusted, this.length));
    this.splice(adjusted, 0, piece.text, piece.spans);
  }

  deleteRange(start: number, end: number): void {
    this.splice(start, end - start, '', []);
  }

  replaceRange(start: number, end: number, source: SourceId, text: string): void {
    this.registry.readersOf(source);
    this.splice(start, end - start, text, [
      { length: text.length, sources: new Set([source]) },
    ]);
  }

  overlay(other: Document): void {
    this.assertSameRegistry(other);
    const mine = this.cells();
    const theirs = other.cells();
    const size = Math.max(mine.length, theirs.length);
    let text = '';
    const spans: Span[] = [];
    for (let i = 0; i < size; i++) {
      const a = mine[i];
      const b = theirs[i];
      const ch = a ? a.ch : b.ch;
      const sources = new Set<SourceId>();
      if (a) for (const s of a.sources) sources.add(s);
      if (b) for (const s of b.sources) sources.add(s);
      text += ch;
      spans.push({ length: 1, sources });
    }
    this.text = text;
    this.spans = spans;
    this.normalize();
  }

  mapChars(fn: (ch: string) => string): void {
    const cells = this.cells();
    let text = '';
    const spans: Span[] = [];
    for (const cell of cells) {
      const mapped = fn(cell.ch);
      if (mapped.length === 0) continue;
      text += mapped;
      spans.push({ length: mapped.length, sources: cell.sources });
    }
    this.text = text;
    this.spans = spans;
    this.normalize();
  }

  collapseWhitespace(): void {
    const cells = this.cells();
    let text = '';
    const spans: Span[] = [];
    let i = 0;
    while (i < cells.length) {
      const cell = cells[i];
      if (!/\s/.test(cell.ch)) {
        text += cell.ch;
        spans.push({ length: 1, sources: cell.sources });
        i++;
        continue;
      }
      const merged = new Set<SourceId>();
      while (i < cells.length && /\s/.test(cells[i].ch)) {
        for (const s of cells[i].sources) merged.add(s);
        i++;
      }
      text += ' ';
      spans.push({ length: 1, sources: merged });
    }
    this.text = text;
    this.spans = spans;
    this.normalize();
  }

  visibleFor(reader: ReaderId): VisibilityReport {
    let text = '';
    const redactions: Redaction[] = [];
    let pos = 0;
    for (const span of this.spans) {
      if (this.registry.isVisible(span.sources, reader)) {
        text += this.text.slice(pos, pos + span.length);
      } else {
        text += REDACTION_CHAR.repeat(span.length);
        const blocking = this.registry.blockingSources(span.sources, reader);
        const last = redactions[redactions.length - 1];
        if (last && last.end === pos) {
          last.end = pos + span.length;
          for (const s of blocking) {
            if (!last.reasons.includes(s)) last.reasons.push(s);
          }
          last.reasons.sort();
        } else {
          redactions.push({ start: pos, end: pos + span.length, reasons: [...blocking] });
        }
      }
      pos += span.length;
    }
    return { text, redactions };
  }

  private cells(): { ch: string; sources: ReadonlySet<SourceId> }[] {
    const out: { ch: string; sources: ReadonlySet<SourceId> }[] = [];
    let pos = 0;
    for (const span of this.spans) {
      for (let i = 0; i < span.length; i++) {
        out.push({ ch: this.text[pos + i], sources: span.sources });
      }
      pos += span.length;
    }
    return out;
  }

  private assertSameRegistry(other: Document): void {
    if (other.registry !== this.registry) {
      throw new Error('documents use different source registries');
    }
  }

  private splitAt(pos: number): number {
    if (pos < 0 || pos > this.length) {
      throw new RangeError(`position out of range: ${pos}`);
    }
    let offset = 0;
    for (let i = 0; i < this.spans.length; i++) {
      const span = this.spans[i];
      if (offset === pos) return i;
      if (offset < pos && pos < offset + span.length) {
        const left: Span = { length: pos - offset, sources: span.sources };
        const right: Span = { length: offset + span.length - pos, sources: span.sources };
        this.spans.splice(i, 1, left, right);
        return i + 1;
      }
      offset += span.length;
    }
    return this.spans.length;
  }

  private splice(
    start: number,
    deleteCount: number,
    insertText: string,
    insertSpans: Span[],
  ): void {
    if (start < 0 || start > this.length) {
      throw new RangeError(`start out of range: ${start}`);
    }
    if (deleteCount < 0 || start + deleteCount > this.length) {
      throw new RangeError(`delete range out of bounds: ${start}+${deleteCount}`);
    }
    const inserted = insertSpans.reduce((n, span) => n + span.length, 0);
    if (inserted !== insertText.length) {
      throw new Error('span/text length mismatch');
    }
    const left = this.splitAt(start);
    const right = this.splitAt(start + deleteCount);
    this.spans = [...this.spans.slice(0, left), ...insertSpans, ...this.spans.slice(right)];
    this.text = this.text.slice(0, start) + insertText + this.text.slice(start + deleteCount);
    this.normalize();
  }

  private extract(start: number, end: number): { text: string; spans: Span[] } {
    if (start < 0 || end < start || end > this.length) {
      throw new RangeError(`range out of bounds: [${start}, ${end})`);
    }
    const left = this.splitAt(start);
    const right = this.splitAt(end);
    return {
      text: this.text.slice(start, end),
      spans: cloneSpans(this.spans.slice(left, right)),
    };
  }

  private normalize(): void {
    const merged: Span[] = [];
    for (const span of this.spans) {
      if (span.length === 0) continue;
      const last = merged[merged.length - 1];
      if (last && spanKey(last.sources) === spanKey(span.sources)) {
        last.length += span.length;
      } else {
        merged.push({ length: span.length, sources: span.sources });
      }
    }
    this.spans = merged;
  }
}
