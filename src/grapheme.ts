// 字形簇切分:优先用 Node 内置 Intl.Segmenter(非浏览器布局引擎);
// 不可用时退化为内置的 emoji 组合簇规则(ZWJ 序列、修饰符、区域指示符、变体选择符、组合记号)。

export function segmentGraphemes(text: string): string[] {
  const Seg = (Intl as { Segmenter?: typeof Intl.Segmenter }).Segmenter;
  if (Seg) {
    const seg = new Seg('und', { granularity: 'grapheme' });
    const out: string[] = [];
    for (const s of seg.segment(text)) out.push(s.segment);
    return out;
  }
  return fallbackSegment(text);
}

const ZWJ = 0x200d;
const VS_RANGE: [number, number][] = [[0xfe00, 0xfe0f], [0xe0100, 0xe01ef]];
const COMBINING: [number, number][] = [
  [0x0300, 0x036f], [0x1ab0, 0x1aff], [0x1dc0, 0x1dff],
  [0x20d0, 0x20ff], [0xfe20, 0xfe2f],
];
const MODIFIER: [number, number][] = [[0x1f3fb, 0x1f3ff]];
const TAG: [number, number][] = [[0xe0020, 0xe007f]];

function inRanges(cp: number, ranges: [number, number][]): boolean {
  return ranges.some(([a, b]) => cp >= a && cp <= b);
}
function isRegional(cp: number): boolean {
  return cp >= 0x1f1e6 && cp <= 0x1f1ff;
}

function fallbackSegment(text: string): string[] {
  const cps = Array.from(text).map((c) => c.codePointAt(0)!);
  const offsets: number[] = [];
  let off = 0;
  for (const c of Array.from(text)) { offsets.push(off); off += c.length; }
  const out: string[] = [];
  let i = 0;
  while (i < cps.length) {
    let j = i + 1;
    // 区域指示符成对(国旗)
    if (isRegional(cps[i]) && j < cps.length && isRegional(cps[j])) j++;
    // 吸收后续:组合记号 / 变体选择符 / 肤色修饰符 / 标签 / ZWJ 序列
    for (;;) {
      if (j < cps.length &&
          (inRanges(cps[j], COMBINING) || inRanges(cps[j], VS_RANGE) ||
           inRanges(cps[j], MODIFIER) || inRanges(cps[j], TAG))) { j++; continue; }
      if (j + 1 < cps.length && cps[j] === ZWJ) {
        j += 2;
        continue;
      }
      break;
    }
    const start = offsets[i];
    const end = j < cps.length ? offsets[j] : text.length;
    out.push(text.slice(start, end));
    i = j;
  }
  return out;
}
