/**
 * 段式来源映射库。
 *
 * 每一轮转换产生一个 SourceMapping：输出文本的半开区间 [outStart, outEnd)
 * 映射到输入文本的半开区间 [inStart, inEnd)。段分三种：
 *
 * - copy   ：等长复制/移动，输出与输入逐字符线性对应，可任意细分；
 * - block  ：替换块，输出整体原子地对应输入整体（长度可不等），不可细分，
 *            部分命中时来源按整个输入区间计；
 * - insert ：纯插入，输入侧退化为零宽锚点 [a, a)。
 *
 * 两轮映射可组合（composeMappings），组合结果同样满足上述不变式。
 */

export type SegmentKind = 'copy' | 'block' | 'insert';

export interface Segment {
  readonly kind: SegmentKind;
  readonly outStart: number;
  readonly outEnd: number;
  readonly inStart: number;
  readonly inEnd: number;
}

/** 半开区间片段 [start, end)。 */
export interface Fragment {
  start: number;
  end: number;
}

export function isInsertion(seg: Segment): boolean {
  return seg.kind === 'insert';
}

export function isCopy(seg: Segment): boolean {
  return seg.kind === 'copy';
}

function compareByOutput(a: Segment, b: Segment): number {
  return a.outStart - b.outStart || a.outEnd - b.outEnd;
}

export class SourceMapping {
  /** 按输出位置稳定排序的段序列（相同输出范围的多值段保持生成顺序）。 */
  readonly segments: readonly Segment[];

  readonly inputLength: number;
  readonly outputLength: number;

  constructor(inputLength: number, outputLength: number, segments: Iterable<Segment>) {
    this.inputLength = inputLength;
    this.outputLength = outputLength;
    const list = [...segments];
    for (const s of list) {
      if (!(0 <= s.inStart && s.inStart <= s.inEnd && s.inEnd <= inputLength)) {
        throw new RangeError(`segment input range [${s.inStart}, ${s.inEnd}) out of bounds (inputLength=${inputLength})`);
      }
      if (!(0 <= s.outStart && s.outStart <= s.outEnd && s.outEnd <= outputLength)) {
        throw new RangeError(`segment output range [${s.outStart}, ${s.outEnd}) out of bounds (outputLength=${outputLength})`);
      }
      if (s.kind === 'insert' && (s.inStart !== s.inEnd || s.outStart === s.outEnd)) {
        throw new RangeError('insert segment must have a zero-width input anchor and non-empty output');
      }
      if (s.kind === 'copy' && s.outEnd - s.outStart !== s.inEnd - s.inStart) {
        throw new RangeError('copy segment must have equal input/output lengths');
      }
      if (s.kind !== 'insert' && (s.outStart === s.outEnd || s.inStart === s.inEnd)) {
        throw new RangeError('copy/block segments must have non-empty input and output ranges');
      }
    }
    this.segments = Object.freeze(list.sort(compareByOutput));
  }
}

/**
 * 把任意顺序到达的片段规范化为确定性的区间集合：
 * - 非零宽片段排序后做区间并（重叠或相接即合并），删除造成的来源空隙
 *   保持断开，绝不跨空隙合并；
 * - 零宽锚点片段（start === end）永不并入区间，仅相同锚点去重，
 *   保证反向零宽查询不会丢失插入锚点；
 * - 结果按 (start, end) 排序 —— 与片段的到达顺序和细分粒度无关。
 */
function canonicalize(frags: Fragment[]): Fragment[] {
  const intervals: Fragment[] = [];
  const anchors = new Set<number>();
  for (const f of frags) {
    if (f.start === f.end) anchors.add(f.start);
    else intervals.push({ start: f.start, end: f.end });
  }
  intervals.sort((a, b) => a.start - b.start || a.end - b.end);
  const merged: Fragment[] = [];
  for (const f of intervals) {
    const last = merged[merged.length - 1];
    if (last && f.start <= last.end) {
      if (f.end > last.end) last.end = f.end;
    } else {
      merged.push(f);
    }
  }
  for (const a of anchors) merged.push({ start: a, end: a });
  return merged.sort((a, b) => a.start - b.start || a.end - b.end);
}

/**
 * 把输出文本中的点 pos 平移到输入文本坐标。
 *
 * 约定：copy 段内线性平移；insert 段内取锚点；block 段内（无逐字符对应）
 * 锚定到块来源起点；落在段间空隙时取前一段的末端锚点；越界时取末段末端（或 0）。
 */
export function translatePoint(mapping: SourceMapping, pos: number): number {
  let fallback = 0;
  for (const s of mapping.segments) {
    if (pos < s.outStart) return fallback;
    if (pos < s.outEnd) {
      if (s.kind === 'insert') return s.inStart;
      if (s.kind === 'copy') return s.inStart + (pos - s.outStart);
      return s.inStart;
    }
    fallback = s.kind === 'insert' ? s.inStart : s.inEnd;
  }
  return fallback;
}

interface Run {
  kind: SegmentKind;
  a0: number;
  a1: number;
  b0: number;
  b1: number;
}

/**
 * 组合两轮映射：inner 为第一轮（中间文本 → 原始输入），outer 为第二轮
 * （最终输出 → 中间文本），返回组合映射（最终输出 → 原始输入）。
 *
 * 规则：
 * - outer insert：锚点经 translatePoint 平移到原始输入，仍为零宽锚点；
 * - outer copy ：按 inner 段边界细分，copy 片段线性平移，block 片段取整个
 *   来源区间，insert 片段退化为零宽锚点；只有两侧都严格相接（来源终点 ==
 *   下一片段来源起点，且中间文本坐标相邻）的 copy 片段才合并，保持线性对应；
 * - outer block：原子处理 —— 其输入范围覆盖到的每个来源连续段各产生一个
 *   以整个输出范围为输出的段（多值），来源空隙不被合并。
 */
export function composeMappings(inner: SourceMapping, outer: SourceMapping): SourceMapping {
  if (outer.inputLength !== inner.outputLength) {
    throw new Error(
      `length mismatch: outer input (${outer.inputLength}) != inner output (${inner.outputLength})`,
    );
  }
  const segments: Segment[] = [];
  for (const o of outer.segments) {
    if (o.kind === 'insert') {
      const anchor = translatePoint(inner, o.inStart);
      segments.push({ kind: 'insert', outStart: o.outStart, outEnd: o.outEnd, inStart: anchor, inEnd: anchor });
      continue;
    }
    const runs: Run[] = [];
    for (const s of inner.segments) {
      const c0 = Math.max(s.outStart, o.inStart);
      const c1 = Math.min(s.outEnd, o.inEnd);
      if (c0 >= c1) continue;
      let a0: number;
      let a1: number;
      if (s.kind === 'insert') {
        a0 = a1 = s.inStart;
      } else if (s.kind === 'copy') {
        a0 = s.inStart + (c0 - s.outStart);
        a1 = s.inStart + (c1 - s.outStart);
      } else {
        a0 = s.inStart;
        a1 = s.inEnd;
      }
      const last = runs[runs.length - 1];
      if (last && last.kind === 'copy' && s.kind === 'copy' && a0 === last.a1 && c0 === last.b1) {
        last.a1 = a1;
        last.b1 = c1;
      } else {
        runs.push({ kind: s.kind, a0, a1, b0: c0, b1: c1 });
      }
    }
    for (const r of runs) {
      if (o.kind === 'copy') {
        segments.push({
          kind: r.kind,
          outStart: o.outStart + (r.b0 - o.inStart),
          outEnd: o.outStart + (r.b1 - o.inStart),
          inStart: r.a0,
          inEnd: r.a1,
        });
      } else {
        segments.push({
          kind: r.kind === 'insert' ? 'insert' : 'block',
          outStart: o.outStart,
          outEnd: o.outEnd,
          inStart: r.a0,
          inEnd: r.a1,
        });
      }
    }
  }
  return new SourceMapping(inner.inputLength, outer.outputLength, segments);
}

/**
 * 正向查询：输出范围 [start, end) 的来源片段，按来源位置有序返回。
 * 重叠/相接的片段合并为连通区间，但绝不跨删除空隙合并。
 * block 段部分命中时返回其整个来源区间；insert 段命中时返回零宽锚点。
 */
export function querySource(mapping: SourceMapping, start: number, end: number): Fragment[] {
  if (start < 0 || end > mapping.outputLength || start > end) {
    throw new RangeError(`query range [${start}, ${end}) out of bounds (outputLength=${mapping.outputLength})`);
  }
  const frags: Fragment[] = [];
  for (const s of mapping.segments) {
    const c0 = Math.max(s.outStart, start);
    const c1 = Math.min(s.outEnd, end);
    if (c0 >= c1) continue;
    if (s.kind === 'insert') {
      frags.push({ start: s.inStart, end: s.inEnd });
    } else if (s.kind === 'copy') {
      frags.push({ start: s.inStart + (c0 - s.outStart), end: s.inStart + (c1 - s.outStart) });
    } else {
      frags.push({ start: s.inStart, end: s.inEnd });
    }
  }
  return canonicalize(frags);
}

/**
 * 反向查询：来源范围 [start, end) 对应的输出范围列表。同一段来源被复制
 * 多次时返回多个不相交的输出范围（多值）。insert 段的锚点落在查询范围内
 * （半开：start <= a < end；零宽查询要求 a === start）时返回其整个输出范围。
 */
export function queryOutput(mapping: SourceMapping, start: number, end: number): Fragment[] {
  if (start < 0 || end > mapping.inputLength || start > end) {
    throw new RangeError(`query range [${start}, ${end}) out of bounds (inputLength=${mapping.inputLength})`);
  }
  const ranges: Fragment[] = [];
  for (const s of mapping.segments) {
    if (s.kind === 'insert') {
      const a = s.inStart;
      const hit = start === end ? a === start : a >= start && a < end;
      if (hit) ranges.push({ start: s.outStart, end: s.outEnd });
      continue;
    }
    const c0 = Math.max(s.inStart, start);
    const c1 = Math.min(s.inEnd, end);
    if (c0 >= c1) continue;
    if (s.kind === 'copy') {
      ranges.push({ start: s.outStart + (c0 - s.inStart), end: s.outStart + (c1 - s.inStart) });
    } else {
      ranges.push({ start: s.outStart, end: s.outEnd });
    }
  }
  return canonicalize(ranges);
}
