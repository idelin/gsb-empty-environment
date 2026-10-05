/**
 * 逐字符参考模型:与 AnnotationStore 完全独立的实现。
 * 文档建模为字符数组,每个区间批注建模为“被覆盖字符下标的集合”,
 * 零宽批注与失效记录建模为间隙位置。编辑通过 splice + 下标重算完成,
 * 用于对区间库做逐位置差分核对。
 */
import type { Affinity, Edit, Segment } from '../src/annotation-store';

interface Affinities {
  sa: Affinity;
  ea: Affinity;
}

function compareIds(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export class ReferenceModel {
  private chars: string[];
  private readonly aff = new Map<string, Affinities>();
  private readonly ranges = new Map<string, Set<number>>(); // id -> 被覆盖字符下标(非空)
  private readonly points = new Map<string, number>(); // id -> 间隙位置(零宽)
  private readonly tombs = new Map<string, number>(); // id -> 间隙位置(失效记录)

  constructor(text = '') {
    this.chars = [...text];
  }

  get text(): string {
    return this.chars.join('');
  }

  get length(): number {
    return this.chars.length;
  }

  add(id: string, start: number, end: number, sa: Affinity, ea: Affinity): void {
    this.aff.set(id, { sa, ea });
    if (start === end) {
      this.points.set(id, start);
    } else {
      const covered = new Set<number>();
      for (let i = start; i < end; i++) covered.add(i);
      this.ranges.set(id, covered);
    }
  }

  remove(id: string): void {
    this.aff.delete(id);
    this.ranges.delete(id);
    this.points.delete(id);
    this.tombs.delete(id);
  }

  applyEdit(start: number, deleteCount: number, insert: string): void {
    const d0 = start;
    const d1 = start + deleteCount;
    const ins = insert.length;

    // 1. 区间批注:幸存字符 + 插入文本是否被吞入。
    for (const [id, covered] of [...this.ranges]) {
      const survivors = new Set<number>();
      for (const i of covered) {
        if (i < d0) survivors.add(i);
        else if (i >= d1) survivors.add(i - deleteCount + ins);
      }
      if (survivors.size === 0) {
        this.ranges.delete(id);
        if (ins > 0) {
          const relocated = new Set<number>();
          for (let i = d0; i < d0 + ins; i++) relocated.add(i);
          this.ranges.set(id, relocated);
        } else {
          this.tombs.set(id, d0);
        }
        continue;
      }
      if (ins > 0) {
        const { sa, ea } = this.aff.get(id)!;
        const leftCovered = covered.has(d0 - 1);
        const rightCovered = covered.has(d1);
        const expand =
          leftCovered && rightCovered
            ? true
            : leftCovered
              ? ea === 'right'
              : rightCovered
                ? sa === 'left'
                : false;
        if (expand) {
          for (let i = d0; i < d0 + ins; i++) survivors.add(i);
        }
      }
      this.ranges.set(id, survivors);
    }

    // 2. 恢复编辑:插入落在失效记录位置上,批注重新定位到插入文本。
    //    复活的批注直接取新区间,不再被本次编辑映射。
    const revived = new Set<string>();
    if (ins > 0) {
      for (const [id, q] of [...this.tombs]) {
        if (q >= d0 && q <= d1) {
          this.tombs.delete(id);
          const covered = new Set<number>();
          for (let i = d0; i < d0 + ins; i++) covered.add(i);
          this.ranges.set(id, covered);
          revived.add(id);
        }
      }
    }

    // 3. 零宽点批注与失效记录:按 startAffinity 做间隙映射。
    const mapGap = (g: number, sa: Affinity): number =>
      g < d0 ? g : g > d1 ? g - deleteCount + ins : sa === 'left' ? d0 : d0 + ins;
    for (const [id, g] of [...this.points]) {
      this.points.set(id, mapGap(g, this.aff.get(id)!.sa));
    }
    for (const [id, g] of [...this.tombs]) {
      if (revived.has(id)) continue;
      this.tombs.set(id, mapGap(g, this.aff.get(id)!.sa));
    }

    // 4. 文本。
    this.chars.splice(d0, deleteCount, ...insert);
  }

  applyEdits(edits: Edit[]): void {
    const sorted = [...edits].sort((a, b) => a.start - b.start);
    for (let i = sorted.length - 1; i >= 0; i--) {
      const e = sorted[i]!;
      this.applyEdit(e.start, e.deleteCount, e.text ?? '');
    }
  }

  replaceAll(search: string, replacement: string): number {
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

  /** 与 store.annotations() 对照的状态表(按 ID 排序)。 */
  state(): Array<{ id: string; start: number; end: number; invalid: boolean }> {
    const out: Array<{ id: string; start: number; end: number; invalid: boolean }> = [];
    for (const [id, covered] of this.ranges) {
      let min = Infinity;
      let max = -Infinity;
      for (const i of covered) {
        if (i < min) min = i;
        if (i > max) max = i;
      }
      out.push({ id, start: min, end: max + 1, invalid: false });
    }
    for (const [id, g] of this.points) out.push({ id, start: g, end: g, invalid: false });
    for (const [id, g] of this.tombs) out.push({ id, start: g, end: g, invalid: true });
    return out.sort((a, b) => compareIds(a.id, b.id));
  }

  /** 逐位置覆盖:覆盖字符 pos 的区间批注 + 落在间隙 pos 的点批注。 */
  coveredAt(pos: number): string[] {
    const ids: string[] = [];
    for (const [id, covered] of this.ranges) {
      if (covered.has(pos)) ids.push(id);
    }
    for (const [id, g] of this.points) {
      if (g === pos) ids.push(id);
    }
    return ids.sort(compareIds);
  }

  tombstones(): Array<{ id: string; pos: number }> {
    return [...this.tombs]
      .map(([id, pos]) => ({ id, pos }))
      .sort((a, b) => compareIds(a.id, b.id));
  }

  segments(): Segment[] {
    const len = this.length;
    const rangeList: Array<{ id: string; s: number; e: number }> = [];
    for (const [id, covered] of this.ranges) {
      let min = Infinity;
      let max = -Infinity;
      for (const i of covered) {
        if (i < min) min = i;
        if (i > max) max = i;
      }
      rangeList.push({ id, s: min, e: max + 1 });
    }
    const pointList = [...this.points].map(([id, g]) => ({ id, g }));
    const bounds = new Set<number>([0, len]);
    for (const r of rangeList) {
      bounds.add(r.s);
      bounds.add(r.e);
    }
    for (const p of pointList) bounds.add(p.g);
    const pts = [...bounds].sort((a, b) => a - b);
    const result: Segment[] = [];
    for (let i = 0; i + 1 < pts.length; i++) {
      const a = pts[i]!;
      const b = pts[i + 1]!;
      const ids = [
        ...rangeList.filter((r) => r.s <= a && r.e >= b).map((r) => r.id),
        ...pointList.filter((p) => a <= p.g && p.g < b).map((p) => p.id),
      ].sort(compareIds);
      result.push({ start: a, end: b, ids });
    }
    const tail = pointList.filter((p) => p.g === len).map((p) => p.id).sort(compareIds);
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
}
