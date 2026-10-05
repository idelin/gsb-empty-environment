import {
  effectiveReaders,
  normalizeSources,
  sameSources,
  type CharCell,
  type ReaderId,
  type SourceId,
  type SourceRegistry,
} from './model.ts';
import { queryCells, type QueryResult } from './query.ts';

interface Run {
  text: string;
  sources: SourceId[];
}

/**
 * 高效实现：按“相同来源集合”的连续区间（run）存储文本，
 * 编辑操作通过区间切分/合并完成，查询时展开为逐字符视图。
 * 语义与 ReferenceDocument 完全一致。
 */
export class FragmentDocument {
  readonly registry: SourceRegistry;
  private runs: Run[] = [];
  private totalLength = 0;

  constructor(registry: SourceRegistry) {
    this.registry = registry;
  }

  get length(): number {
    return this.totalLength;
  }

  private checkPos(pos: number): void {
    if (!Number.isInteger(pos) || pos < 0 || pos > this.totalLength) {
      throw new RangeError(`position ${pos} out of bounds (length ${this.totalLength})`);
    }
  }

  private checkRange(start: number, end: number): void {
    this.checkPos(start);
    this.checkPos(end);
    if (start > end) throw new RangeError(`invalid range [${start}, ${end})`);
  }

  private splitAt(pos: number): void {
    if (pos <= 0 || pos >= this.totalLength) return;
    let acc = 0;
    for (let i = 0; i < this.runs.length; i++) {
      const run = this.runs[i];
      if (acc + run.text.length > pos) {
        const offset = pos - acc;
        this.runs.splice(
          i,
          1,
          { text: run.text.slice(0, offset), sources: run.sources },
          { text: run.text.slice(offset), sources: run.sources },
        );
        return;
      }
      acc += run.text.length;
    }
  }

  private coalesce(): void {
    const merged: Run[] = [];
    for (const run of this.runs) {
      const last = merged[merged.length - 1];
      if (last && sameSources(last.sources, run.sources)) {
        merged[merged.length - 1] = { text: last.text + run.text, sources: last.sources };
      } else {
        merged.push({ text: run.text, sources: [...run.sources] });
      }
    }
    this.runs = merged;
  }

  private spliceRuns(start: number, deleteCount: number, inserted: Run[]): void {
    const end = start + deleteCount;
    this.splitAt(start);
    this.splitAt(end);
    let acc = 0;
    let i = 0;
    while (i < this.runs.length && acc < start) {
      acc += this.runs[i].text.length;
      i++;
    }
    let j = i;
    let removed = 0;
    while (j < this.runs.length && removed < deleteCount) {
      removed += this.runs[j].text.length;
      j++;
    }
    const clean = inserted
      .filter((r) => r.text.length > 0)
      .map((r) => ({ text: r.text, sources: [...r.sources] }));
    this.runs.splice(i, j - i, ...clean);
    this.totalLength += clean.reduce((n, r) => n + r.text.length, 0) - deleteCount;
    this.coalesce();
  }

  private extractRuns(start: number, end: number): Run[] {
    this.splitAt(start);
    this.splitAt(end);
    const out: Run[] = [];
    let acc = 0;
    for (const run of this.runs) {
      const runEnd = acc + run.text.length;
      if (runEnd > start && acc < end) out.push({ text: run.text, sources: [...run.sources] });
      acc = runEnd;
    }
    return out;
  }

  private sourcesAt(pos: number): readonly SourceId[] {
    let acc = 0;
    for (const run of this.runs) {
      if (pos < acc + run.text.length) return run.sources;
      acc += run.text.length;
    }
    throw new RangeError(`position ${pos} out of bounds (length ${this.totalLength})`);
  }

  private neighborSources(pos: number): SourceId[] {
    const set = new Set<SourceId>();
    if (pos > 0) for (const s of this.sourcesAt(pos - 1)) set.add(s);
    if (pos < this.totalLength) for (const s of this.sourcesAt(pos)) set.add(s);
    return normalizeSources(set);
  }

  private unionSources(start: number, end: number): SourceId[] {
    if (start === end) return this.neighborSources(start);
    const set = new Set<SourceId>();
    for (const run of this.extractRuns(start, end)) {
      for (const s of run.sources) set.add(s);
    }
    return normalizeSources(set);
  }

  append(sourceId: SourceId, text: string): void {
    this.insert(this.totalLength, sourceId, text);
  }

  insert(pos: number, sourceId: SourceId, text: string): void {
    this.registry.get(sourceId);
    this.checkPos(pos);
    if (text.length === 0) return;
    this.spliceRuns(pos, 0, [{ text, sources: [sourceId] }]);
  }

  copyRange(start: number, end: number, dest: number): void {
    this.checkRange(start, end);
    this.checkPos(dest);
    if (start === end) return;
    const runs = this.extractRuns(start, end);
    this.spliceRuns(dest, 0, runs);
  }

  moveRange(start: number, end: number, dest: number): void {
    this.checkRange(start, end);
    this.checkPos(dest);
    if (start === end) return;
    const runs = this.extractRuns(start, end);
    this.spliceRuns(start, end - start, []);
    this.spliceRuns(Math.min(dest, this.totalLength), 0, runs);
  }

  deleteRange(start: number, end: number): void {
    this.checkRange(start, end);
    this.spliceRuns(start, end - start, []);
  }

  replaceRange(start: number, end: number, text: string): void {
    this.checkRange(start, end);
    const sources = this.unionSources(start, end);
    this.spliceRuns(start, end - start, text.length > 0 ? [{ text, sources }] : []);
  }

  mergeRange(start: number, end: number, newSourceId: SourceId): void {
    this.checkRange(start, end);
    if (start === end) return;
    const runs = this.extractRuns(start, end);
    const involved = normalizeSources(runs.flatMap((r) => r.sources));
    const readers =
      involved.length === 0
        ? this.registry.allReaders()
        : effectiveReaders(this.registry, involved)!;
    this.registry.addSource(newSourceId, readers, involved);
    const merged = runs.map((r) => ({
      text: r.text,
      sources: normalizeSources([...r.sources, newSourceId]),
    }));
    this.spliceRuns(start, end - start, merged);
  }

  insertFormatting(pos: number, text: string): void {
    this.checkPos(pos);
    if (text.length === 0) return;
    this.spliceRuns(pos, 0, [{ text, sources: this.neighborSources(pos) }]);
  }

  mapRange(start: number, end: number, fn: (ch: string) => string): void {
    this.checkRange(start, end);
    if (start === end) return;
    const runs = this.extractRuns(start, end);
    const mapped = runs.map((r) => ({
      text: [...r.text].map((ch) => fn(ch)).join(''),
      sources: r.sources,
    }));
    this.spliceRuns(start, end - start, mapped);
  }

  text(): string {
    return this.runs.map((r) => r.text).join('');
  }

  expand(): CharCell[] {
    const cells: CharCell[] = [];
    for (const run of this.runs) {
      for (const ch of run.text) cells.push({ ch, sources: [...run.sources] });
    }
    return cells;
  }

  queryReader(reader: ReaderId, redactionChar?: string): QueryResult {
    return queryCells(this.registry, this.expand(), reader, redactionChar);
  }
}
