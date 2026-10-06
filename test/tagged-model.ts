/**
 * 逐字符带来源标签的独立模型，用于交叉核对 src/mapping.ts。
 *
 * 每个字符携带 frags：若对该字符做点查询，期望得到的原始输入半开区间列表。
 * - 复制字符 i        → [{ i, i+1 }]
 * - 替换产生的新字符   → 被替换字符来源区间的并集（按序相接合并）
 * - 纯插入字符         → 零宽锚点 [{ a, a }]
 *
 * 模型的合并规则与库一致：片段集合规范化为有序不相交区间（重叠/相接合并，
 * 删除空隙保持断开）；零宽锚点片段永不并入区间（仅相同锚点去重）。
 */

import { SourceMapping, type Fragment, type Segment } from '../src/mapping.ts';

export interface ModelChar {
  ch: string;
  frags: Fragment[];
}

export class TaggedText {
  readonly chars: ModelChar[];

  constructor(chars: ModelChar[]) {
    this.chars = chars;
  }

  static source(text: string): TaggedText {
    return new TaggedText([...text].map((ch, i) => ({ ch, frags: [{ start: i, end: i + 1 }] })));
  }

  get length(): number {
    return this.chars.length;
  }

  get text(): string {
    return this.chars.map((c) => c.ch).join('');
  }
}

export type Op =
  | { type: 'copy'; start: number; end: number }
  | { type: 'replace'; start: number; end: number; text: string }
  | { type: 'insert'; pos: number; text: string };

export function mergeFrags(frags: Fragment[]): Fragment[] {
  const intervals: Fragment[] = [];
  const anchors = new Set<number>();
  for (const f of frags) {
    if (f.start === f.end) anchors.add(f.start);
    else intervals.push({ start: f.start, end: f.end });
  }
  intervals.sort((a, b) => a.start - b.start || a.end - b.end);
  const out: Fragment[] = [];
  for (const f of intervals) {
    const last = out[out.length - 1];
    if (last && f.start <= last.end) {
      if (f.end > last.end) last.end = f.end;
    } else {
      out.push(f);
    }
  }
  for (const a of anchors) out.push({ start: a, end: a });
  return out.sort((a, b) => a.start - b.start || a.end - b.end);
}

function cloneFrags(frags: Fragment[]): Fragment[] {
  return frags.map((f) => ({ start: f.start, end: f.end }));
}

/** 插入锚点的模型语义：取 pos 处字符的首个来源起点；文末取末字符来源末端。 */
function anchorFrags(input: TaggedText, pos: number): Fragment[] {
  if (input.length === 0) return [{ start: 0, end: 0 }];
  if (pos >= input.length) {
    const last = input.chars[input.length - 1].frags;
    const end = last[last.length - 1].end;
    return [{ start: end, end }];
  }
  const first = input.chars[pos].frags[0];
  return [{ start: first.start, end: first.start }];
}

/** 应用一轮转换操作，同时产出带标签的新文本与该轮的 SourceMapping。 */
export function applyOps(input: TaggedText, ops: Op[]): { text: TaggedText; mapping: SourceMapping } {
  const chars: ModelChar[] = [];
  const segments: Segment[] = [];
  for (const op of ops) {
    const outStart = chars.length;
    if (op.type === 'copy') {
      if (!(0 <= op.start && op.start < op.end && op.end <= input.length)) {
        throw new RangeError(`bad copy op [${op.start}, ${op.end})`);
      }
      for (let i = op.start; i < op.end; i++) {
        chars.push({ ch: input.chars[i].ch, frags: cloneFrags(input.chars[i].frags) });
      }
      segments.push({ kind: 'copy', outStart, outEnd: chars.length, inStart: op.start, inEnd: op.end });
    } else if (op.type === 'replace') {
      if (!(0 <= op.start && op.start < op.end && op.end <= input.length)) {
        throw new RangeError(`bad replace op [${op.start}, ${op.end})`);
      }
      if (op.text.length === 0) continue; // 空替换即删除，不产生输出段
      const union = mergeFrags(input.chars.slice(op.start, op.end).flatMap((c) => c.frags));
      for (const ch of op.text) chars.push({ ch, frags: cloneFrags(union) });
      segments.push({ kind: 'block', outStart, outEnd: chars.length, inStart: op.start, inEnd: op.end });
    } else {
      if (!(0 <= op.pos && op.pos <= input.length)) {
        throw new RangeError(`bad insert op at ${op.pos}`);
      }
      if (op.text.length === 0) continue;
      const anchor = anchorFrags(input, op.pos);
      for (const ch of op.text) chars.push({ ch, frags: cloneFrags(anchor) });
      segments.push({ kind: 'insert', outStart, outEnd: chars.length, inStart: op.pos, inEnd: op.pos });
    }
  }
  return { text: new TaggedText(chars), mapping: new SourceMapping(input.length, chars.length, segments) };
}

/** 模型期望：输出范围 [q0, q1) 的正向查询结果（逐字符标签按序合并）。 */
export function expectedForward(text: TaggedText, q0: number, q1: number): Fragment[] {
  return mergeFrags(text.chars.slice(q0, q1).flatMap((c) => c.frags));
}

/** 模型期望：来源范围 [s0, s1) 的反向查询结果（命中字符的输出区间按序合并）。 */
export function expectedReverse(text: TaggedText, s0: number, s1: number): Fragment[] {
  const hit = (f: Fragment): boolean => {
    if (s0 === s1) return f.start === f.end && f.start === s0;
    return f.start === f.end ? f.start >= s0 && f.start < s1 : f.start < s1 && s0 < f.end;
  };
  const ranges: Fragment[] = [];
  for (let j = 0; j < text.length; j++) {
    if (text.chars[j].frags.some(hit)) {
      const last = ranges[ranges.length - 1];
      if (last && j <= last.end) {
        last.end = j + 1;
      } else {
        ranges.push({ start: j, end: j + 1 });
      }
    }
  }
  return ranges;
}
