/**
 * annotation-intervals — 多层叠加批注区间库。
 *
 * 语义约定:
 * - 文档是字符串,位置是字符间隙(0..length),区间半开 [start, end)。
 * - 零宽批注(start === end)是附着在间隙上的点批注。
 * - 左右黏附位(affinity)决定边界插入是否扩展批注:
 *   - startAffinity 'left' : 起点黏住左侧字符,在起点处插入的文本被吞入批注;
 *                            'right': 起点黏住右侧字符,插入文本留在批注之外。
 *   - endAffinity   'right': 终点黏住右侧字符,在终点处插入的文本被吞入批注;
 *                            'left' : 终点黏住左侧字符,插入文本留在批注之外。
 *   默认 startAffinity='left', endAffinity='right'(两端均扩展,类似 Word 批注)。
 * - 编辑把批注整个非空区间完全删除时,批注不失效删除,而是保留一条失效记录
 *   (tombstone,坍缩到删除点);后续在该位置重新插入文本(如撤销/恢复编辑)
 *   时批注自动重新定位到插入的文本上。
 */

export type Affinity = 'left' | 'right';

export interface Edit {
  start: number;
  deleteCount: number;
  text?: string;
}

export interface AnnotationInput {
  id: string;
  start: number;
  end: number;
  startAffinity?: Affinity;
  endAffinity?: Affinity;
  data?: unknown;
}

export interface Annotation {
  id: string;
  start: number;
  end: number;
  startAffinity: Affinity;
  endAffinity: Affinity;
  /** 编辑删除了整个批注区间后为 true,此时 start === end 为失效记录位置。 */
  invalid: boolean;
  /** 失效前的区间,便于审计/恢复。 */
  invalidatedFrom?: { start: number; end: number };
  data?: unknown;
}

export interface Segment {
  start: number;
  end: number;
  /** 覆盖该段的活跃批注 ID,按 ID 排序。 */
  ids: string[];
}

export const DEFAULT_START_AFFINITY: Affinity = 'left';
export const DEFAULT_END_AFFINITY: Affinity = 'right';

/** 把单个位置按黏附位映射通过一次编辑。 */
export function mapPositionThroughEdit(pos: number, affinity: Affinity, edit: Edit): number {
  const d0 = edit.start;
  const d1 = edit.start + edit.deleteCount;
  const inserted = (edit.text ?? '').length;
  if (pos < d0) return pos;
  if (pos > d1) return pos - edit.deleteCount + inserted;
  return affinity === 'left' ? d0 : d0 + inserted;
}

function compareIds(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function byId(a: Annotation, b: Annotation): number {
  return compareIds(a.id, b.id);
}

function clone(ann: Annotation): Annotation {
  return {
    ...ann,
    invalidatedFrom: ann.invalidatedFrom ? { ...ann.invalidatedFrom } : undefined,
  };
}

function assertPosition(value: number, name: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new RangeError(`${name} must be a non-negative integer, got ${value}`);
  }
}

export class AnnotationStore {
  private text: string;
  private readonly anns = new Map<string, Annotation>();

  constructor(text = '') {
    this.text = text;
  }

  get length(): number {
    return this.text.length;
  }

  getText(): string {
    return this.text;
  }

  add(input: AnnotationInput): Annotation {
    if (this.anns.has(input.id)) {
      throw new Error(`duplicate annotation id: ${input.id}`);
    }
    assertPosition(input.start, 'start');
    assertPosition(input.end, 'end');
    if (input.start > input.end) {
      throw new RangeError(`start (${input.start}) must be <= end (${input.end})`);
    }
    if (input.end > this.text.length) {
      throw new RangeError(`end (${input.end}) exceeds document length (${this.text.length})`);
    }
    const ann: Annotation = {
      id: input.id,
      start: input.start,
      end: input.end,
      startAffinity: input.startAffinity ?? DEFAULT_START_AFFINITY,
      endAffinity: input.endAffinity ?? DEFAULT_END_AFFINITY,
      invalid: false,
      data: input.data,
    };
    this.anns.set(ann.id, ann);
    return clone(ann);
  }

  get(id: string): Annotation | undefined {
    const ann = this.anns.get(id);
    return ann ? clone(ann) : undefined;
  }

  /** 显式移除批注(不留失效记录;失效记录只由“编辑删除整个区间”产生)。 */
  remove(id: string): boolean {
    return this.anns.delete(id);
  }

  /** 全部批注(含失效记录),按 ID 排序。 */
  annotations(): Annotation[] {
    return [...this.anns.values()].sort(byId).map(clone);
  }

  /** 活跃批注,按 ID 排序。 */
  activeAnnotations(): Annotation[] {
    return [...this.anns.values()].filter((a) => !a.invalid).sort(byId).map(clone);
  }

  /** 失效记录(被编辑整体删除的批注),按 ID 排序。 */
  tombstones(): Annotation[] {
    return [...this.anns.values()].filter((a) => a.invalid).sort(byId).map(clone);
  }

  /**
   * 逐位置覆盖查询:返回覆盖字符位置 pos 的活跃批注(半开区间 start <= pos < end),
   * 以及恰好落在间隙 pos 上的零宽点批注;pos 可等于 length(只匹配点批注)。
   */
  annotationsAt(pos: number): Annotation[] {
    assertPosition(pos, 'pos');
    if (pos > this.text.length) {
      throw new RangeError(`pos (${pos}) exceeds document length (${this.text.length})`);
    }
    return [...this.anns.values()]
      .filter((a) => !a.invalid)
      .filter((a) => (a.start === a.end ? a.start === pos : a.start <= pos && pos < a.end))
      .sort(byId)
      .map(clone);
  }

  /** 范围查询:与 [start, end) 相交的活跃批注;start === end 时退化为逐位置查询。 */
  annotationsInRange(start: number, end: number): Annotation[] {
    assertPosition(start, 'start');
    assertPosition(end, 'end');
    if (start > end) {
      throw new RangeError(`start (${start}) must be <= end (${end})`);
    }
    if (end > this.text.length) {
      throw new RangeError(`end (${end}) exceeds document length (${this.text.length})`);
    }
    if (start === end) return this.annotationsAt(start);
    return [...this.anns.values()]
      .filter((a) => !a.invalid)
      .filter((a) =>
        a.start === a.end ? start <= a.start && a.start < end : a.start < end && start < a.end,
      )
      .sort(byId)
      .map(clone);
  }

  /**
   * 按所有活跃批注边界把文档切成基本段,输出每段的活跃批注 ID 集合(按 ID 排序)。
   * 零宽批注归入其位置所在的段;位于文档末尾的零宽批注归入最后一段。
   */
  segments(): Segment[] {
    const len = this.text.length;
    const active = [...this.anns.values()].filter((a) => !a.invalid);
    const bounds = new Set<number>([0, len]);
    for (const a of active) {
      bounds.add(a.start);
      bounds.add(a.end);
    }
    const pts = [...bounds].sort((x, y) => x - y);
    const result: Segment[] = [];
    for (let i = 0; i + 1 < pts.length; i++) {
      const a = pts[i]!;
      const b = pts[i + 1]!;
      const ids = active
        .filter(
          (ann) =>
            (ann.start < ann.end && ann.start <= a && ann.end >= b) ||
            (ann.start === ann.end && a <= ann.start && ann.start < b),
        )
        .map((ann) => ann.id)
        .sort(compareIds);
      result.push({ start: a, end: b, ids });
    }
    const tail = active
      .filter((ann) => ann.start === ann.end && ann.start === len)
      .map((ann) => ann.id)
      .sort(compareIds);
    if (tail.length > 0) {
      const last = result[result.length - 1];
      if (last && last.end === len) {
        last.ids = [...last.ids, ...tail].sort(compareIds);
      } else {
        result.push({ start: len, end: len, ids: tail });
      }
    }
    return result;
  }

  /** 应用单次编辑:更新文本并把全部批注区间映射通过该编辑。 */
  applyEdit(edit: Edit): void {
    const inserted = edit.text ?? '';
    this.validateEdit(edit);
    const d0 = edit.start;
    const d1 = edit.start + edit.deleteCount;
    const ins = inserted.length;
    this.text = this.text.slice(0, d0) + inserted + this.text.slice(d1);
    for (const ann of this.anns.values()) {
      if (ann.invalid) {
        // 失效记录:恢复编辑(在其位置插入文本)可让批注重新定位。
        if (ins > 0 && ann.start >= d0 && ann.start <= d1) {
          ann.start = d0;
          ann.end = d0 + ins;
          ann.invalid = false;
          ann.invalidatedFrom = undefined;
        } else {
          const p = mapPositionThroughEdit(ann.start, ann.startAffinity, edit);
          ann.start = p;
          ann.end = p;
        }
        continue;
      }
      if (ann.start === ann.end) {
        // 零宽点批注:作为整体按 startAffinity 映射,保持零宽。
        const p = mapPositionThroughEdit(ann.start, ann.startAffinity, edit);
        ann.start = p;
        ann.end = p;
        continue;
      }
      if (d0 <= ann.start && ann.end <= d1) {
        // 编辑的删除范围完全覆盖批注区间。
        if (ins === 0) {
          ann.invalidatedFrom = { start: ann.start, end: ann.end };
          ann.start = d0;
          ann.end = d0;
          ann.invalid = true;
        } else {
          // 整体替换:批注迁移到替换文本上。
          ann.start = d0;
          ann.end = d0 + ins;
        }
        continue;
      }
      ann.start = mapPositionThroughEdit(ann.start, ann.startAffinity, edit);
      ann.end = mapPositionThroughEdit(ann.end, ann.endAffinity, edit);
    }
  }

  /**
   * 批量编辑:所有编辑以编辑前坐标给出,必须互不重叠
   * (允许首尾相接;不允许两个纯插入落在同一位置)。
   */
  applyEdits(edits: Edit[]): void {
    const sorted = edits.map((e) => ({ ...e, text: e.text ?? '' })).sort((a, b) => a.start - b.start);
    const len = this.text.length;
    for (let i = 0; i < sorted.length; i++) {
      const e = sorted[i]!;
      assertPosition(e.start, 'edit.start');
      assertPosition(e.deleteCount, 'edit.deleteCount');
      if (e.start + e.deleteCount > len) {
        throw new RangeError(
          `edit range [${e.start}, ${e.start + e.deleteCount}) exceeds document length (${len})`,
        );
      }
      if (i > 0) {
        const prev = sorted[i - 1]!;
        if (prev.start + prev.deleteCount > e.start) {
          throw new Error('overlapping edits in batch');
        }
        if (prev.deleteCount === 0 && e.deleteCount === 0 && prev.start === e.start) {
          throw new Error('ambiguous batch: two pure insertions at the same position');
        }
      }
    }
    for (let i = sorted.length - 1; i >= 0; i--) {
      this.applyEdit(sorted[i]!);
    }
  }

  /** 批量替换:把 search 的所有不重叠出现替换为 replacement,返回替换次数。 */
  replaceAll(search: string, replacement: string): number {
    if (search.length === 0) {
      throw new Error('search must not be empty');
    }
    const edits: Edit[] = [];
    let from = 0;
    for (;;) {
      const idx = this.text.indexOf(search, from);
      if (idx === -1) break;
      edits.push({ start: idx, deleteCount: search.length, text: replacement });
      from = idx + search.length;
    }
    if (edits.length > 0) this.applyEdits(edits);
    return edits.length;
  }

  private validateEdit(edit: Edit): void {
    assertPosition(edit.start, 'edit.start');
    assertPosition(edit.deleteCount, 'edit.deleteCount');
    if (edit.start + edit.deleteCount > this.text.length) {
      throw new RangeError(
        `edit range [${edit.start}, ${edit.start + edit.deleteCount}) exceeds document length (${this.text.length})`,
      );
    }
  }
}
