import {
  effectiveReaders,
  normalizeSources,
  type CharCell,
  type ReaderId,
  type SourceId,
  type SourceRegistry,
} from './model.ts';
import { queryCells, type QueryResult } from './query.ts';

interface MutableCell {
  ch: string;
  sources: SourceId[];
}

/**
 * 逐字符来源集合参考实现：每个字符显式保存其来源片段集合，
 * 用于验证高效实现（FragmentDocument）在各种编辑操作下的正确性。
 */
export class ReferenceDocument {
  readonly registry: SourceRegistry;
  private cells: MutableCell[] = [];

  constructor(registry: SourceRegistry) {
    this.registry = registry;
  }

  get length(): number {
    return this.cells.length;
  }

  private checkPos(pos: number): void {
    if (!Number.isInteger(pos) || pos < 0 || pos > this.cells.length) {
      throw new RangeError(`position ${pos} out of bounds (length ${this.cells.length})`);
    }
  }

  private checkRange(start: number, end: number): void {
    this.checkPos(start);
    this.checkPos(end);
    if (start > end) throw new RangeError(`invalid range [${start}, ${end})`);
  }

  private makeCells(text: string, sources: SourceId[]): MutableCell[] {
    return [...text].map((ch) => ({ ch, sources: [...sources] }));
  }

  private neighborSources(pos: number): SourceId[] {
    const set = new Set<SourceId>();
    if (pos > 0) for (const s of this.cells[pos - 1].sources) set.add(s);
    if (pos < this.cells.length) for (const s of this.cells[pos].sources) set.add(s);
    return normalizeSources(set);
  }

  private unionSources(start: number, end: number): SourceId[] {
    if (start === end) return this.neighborSources(start);
    const set = new Set<SourceId>();
    for (let i = start; i < end; i++) {
      for (const s of this.cells[i].sources) set.add(s);
    }
    return normalizeSources(set);
  }

  append(sourceId: SourceId, text: string): void {
    this.insert(this.cells.length, sourceId, text);
  }

  insert(pos: number, sourceId: SourceId, text: string): void {
    this.registry.get(sourceId);
    this.checkPos(pos);
    if (text.length === 0) return;
    this.cells.splice(pos, 0, ...this.makeCells(text, [sourceId]));
  }

  /** 复制 [start, end) 到 dest（dest 基于复制前的坐标系），来源集合随字符一起复制。 */
  copyRange(start: number, end: number, dest: number): void {
    this.checkRange(start, end);
    this.checkPos(dest);
    if (start === end) return;
    const copies = this.cells
      .slice(start, end)
      .map((c) => ({ ch: c.ch, sources: [...c.sources] }));
    this.cells.splice(dest, 0, ...copies);
  }

  /** 移动 [start, end) 到 dest（dest 基于删除后的坐标系）。 */
  moveRange(start: number, end: number, dest: number): void {
    this.checkRange(start, end);
    this.checkPos(dest);
    if (start === end) return;
    const moved = this.cells.slice(start, end);
    this.cells.splice(start, end - start);
    const d = Math.min(dest, this.cells.length);
    this.cells.splice(d, 0, ...moved);
  }

  deleteRange(start: number, end: number): void {
    this.checkRange(start, end);
    this.cells.splice(start, end - start);
  }

  /**
   * 用新文本替换 [start, end)。新字符的来源 = 被替换字符来源的并集，
   * 因此跨权限边界替换时新文本自动获得各来源读者集合的交集。
   * 空范围替换等价于 insertFormatting（继承相邻字符来源）。
   */
  replaceRange(start: number, end: number, text: string): void {
    this.checkRange(start, end);
    const sources = this.unionSources(start, end);
    this.cells.splice(start, end - start, ...this.makeCells(text, sources));
  }

  /**
   * 将 [start, end) 内的片段合并为一个新来源片段。
   * 新来源的读者集合 = 范围内全部来源读者集合的交集（不为范围内字符放宽权限），
   * 范围内每个字符的来源集合追加新来源 id，保留原始溯源信息。
   */
  mergeRange(start: number, end: number, newSourceId: SourceId): void {
    this.checkRange(start, end);
    if (start === end) return;
    const involved = this.unionSources(start, end);
    const readers =
      involved.length === 0
        ? this.registry.allReaders()
        : effectiveReaders(this.registry, involved)!;
    this.registry.addSource(newSourceId, readers, involved);
    for (let i = start; i < end; i++) {
      const cell = this.cells[i];
      this.cells[i] = { ch: cell.ch, sources: normalizeSources([...cell.sources, newSourceId]) };
    }
  }

  /** 插入格式化文本（空白、缩进、换行等），来源继承相邻字符的并集，不丢失限制。 */
  insertFormatting(pos: number, text: string): void {
    this.checkPos(pos);
    if (text.length === 0) return;
    this.cells.splice(pos, 0, ...this.makeCells(text, this.neighborSources(pos)));
  }

  /** 逐字符映射（大小写转换、展开、删除等），输出字符继承原字符的来源集合。 */
  mapRange(start: number, end: number, fn: (ch: string) => string): void {
    this.checkRange(start, end);
    const out: MutableCell[] = [];
    for (let i = start; i < end; i++) {
      const cell = this.cells[i];
      for (const ch of fn(cell.ch)) out.push({ ch, sources: [...cell.sources] });
    }
    this.cells.splice(start, end - start, ...out);
  }

  text(): string {
    return this.cells.map((c) => c.ch).join('');
  }

  expand(): CharCell[] {
    return this.cells.map((c) => ({ ch: c.ch, sources: [...c.sources] }));
  }

  queryReader(reader: ReaderId, redactionChar?: string): QueryResult {
    return queryCells(this.registry, this.expand(), reader, redactionChar);
  }
}
