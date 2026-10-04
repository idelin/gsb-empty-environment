import { REDACTION_CHAR, SourceRegistry } from './provenance.ts';
import type { ReaderId, Redaction, SourceId, VisibilityReport } from './provenance.ts';

interface RefCell {
  ch: string;
  sources: SourceId[];
}

function cloneCell(cell: RefCell): RefCell {
  return { ch: cell.ch, sources: [...cell.sources] };
}

function unionSources(a: SourceId[], b: SourceId[]): SourceId[] {
  return [...new Set([...a, ...b])].sort();
}

export class ReferenceDocument {
  private cells: RefCell[] = [];

  readonly registry: SourceRegistry;

  constructor(registry: SourceRegistry) {
    this.registry = registry;
  }

  static fromFragment(registry: SourceRegistry, source: SourceId, text: string): ReferenceDocument {
    const doc = new ReferenceDocument(registry);
    doc.appendFragment(source, text);
    return doc;
  }

  get length(): number {
    return this.cells.length;
  }

  toString(): string {
    return this.cells.map((cell) => cell.ch).join('');
  }

  appendFragment(source: SourceId, text: string): void {
    this.insertFragment(this.length, source, text);
  }

  insertFragment(pos: number, source: SourceId, text: string): void {
    this.registry.readersOf(source);
    this.insertCells(
      pos,
      text.split('').map((ch) => ({ ch, sources: [source] })),
    );
  }

  insertLiteral(pos: number, text: string): void {
    this.insertCells(
      pos,
      text.split('').map((ch) => ({ ch, sources: [] })),
    );
  }

  insertDocument(pos: number, other: ReferenceDocument): void {
    this.assertSameRegistry(other);
    this.insertCells(pos, other.cells.map(cloneCell));
  }

  copyRange(start: number, end: number, dest: number): void {
    this.checkRange(start, end);
    this.insertCells(dest, this.cells.slice(start, end).map(cloneCell));
  }

  moveRange(start: number, end: number, dest: number): void {
    this.checkRange(start, end);
    const piece = this.cells.slice(start, end).map(cloneCell);
    this.cells.splice(start, end - start);
    let adjusted = dest;
    if (dest > start) adjusted = dest >= end ? dest - (end - start) : start;
    adjusted = Math.max(0, Math.min(adjusted, this.cells.length));
    this.cells.splice(adjusted, 0, ...piece);
  }

  deleteRange(start: number, end: number): void {
    this.checkRange(start, end);
    this.cells.splice(start, end - start);
  }

  replaceRange(start: number, end: number, source: SourceId, text: string): void {
    this.checkRange(start, end);
    this.registry.readersOf(source);
    this.cells.splice(
      start,
      end - start,
      ...text.split('').map((ch) => ({ ch, sources: [source] })),
    );
  }

  overlay(other: ReferenceDocument): void {
    this.assertSameRegistry(other);
    const size = Math.max(this.cells.length, other.cells.length);
    const merged: RefCell[] = [];
    for (let i = 0; i < size; i++) {
      const a = this.cells[i];
      const b = other.cells[i];
      merged.push({
        ch: a ? a.ch : b.ch,
        sources: unionSources(a ? a.sources : [], b ? b.sources : []),
      });
    }
    this.cells = merged;
  }

  mapChars(fn: (ch: string) => string): void {
    const next: RefCell[] = [];
    for (const cell of this.cells) {
      for (const ch of fn(cell.ch).split('')) {
        next.push({ ch, sources: [...cell.sources] });
      }
    }
    this.cells = next;
  }

  collapseWhitespace(): void {
    const next: RefCell[] = [];
    let i = 0;
    while (i < this.cells.length) {
      const cell = this.cells[i];
      if (!/\s/.test(cell.ch)) {
        next.push({ ch: cell.ch, sources: [...cell.sources] });
        i++;
        continue;
      }
      let merged: SourceId[] = [];
      while (i < this.cells.length && /\s/.test(this.cells[i].ch)) {
        merged = unionSources(merged, this.cells[i].sources);
        i++;
      }
      next.push({ ch: ' ', sources: merged });
    }
    this.cells = next;
  }

  visibleFor(reader: ReaderId): VisibilityReport {
    let text = '';
    const redactions: Redaction[] = [];
    for (let i = 0; i < this.cells.length; i++) {
      const cell = this.cells[i];
      const sources = new Set(cell.sources);
      if (this.registry.isVisible(sources, reader)) {
        text += cell.ch;
        continue;
      }
      text += REDACTION_CHAR;
      const blocking = this.registry.blockingSources(sources, reader);
      const last = redactions[redactions.length - 1];
      if (last && last.end === i) {
        last.end = i + 1;
        for (const s of blocking) {
          if (!last.reasons.includes(s)) last.reasons.push(s);
        }
        last.reasons.sort();
      } else {
        redactions.push({ start: i, end: i + 1, reasons: blocking });
      }
    }
    return { text, redactions };
  }

  private insertCells(pos: number, cells: RefCell[]): void {
    if (pos < 0 || pos > this.cells.length) {
      throw new RangeError(`position out of range: ${pos}`);
    }
    this.cells.splice(pos, 0, ...cells);
  }

  private checkRange(start: number, end: number): void {
    if (start < 0 || end < start || end > this.cells.length) {
      throw new RangeError(`range out of bounds: [${start}, ${end})`);
    }
  }

  private assertSameRegistry(other: ReferenceDocument): void {
    if (other.registry !== this.registry) {
      throw new Error('documents use different source registries');
    }
  }
}
