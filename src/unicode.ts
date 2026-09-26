/**
 * Unicode 测量与范围映射原语（零第三方依赖）。
 *
 * - 规范化：String.prototype.normalize。
 * - 字形簇：Intl.Segmenter（granularity: "grapheme"）。
 * - 规范化串 -> 原串的范围映射：对“原串前 i 个码点组成的前缀”分别规范化，
 *   以前缀规范化结果的 UTF-16 长度构造单调不减边界数组。这样即便规范化让
 *   多个原码点组合成同一个字符（如 e + U+0301 -> é），映射也会回退到覆盖
 *   整组原码点的区间，而不会错位或丢失组合记号。
 */

export type NormalizationForm = "none" | "NFC" | "NFD" | "NFKC" | "NFKD";

export type LengthUnit = "codepoints" | "graphemes" | "utf16";

/** 原文上的一个区间，以 UTF-16 偏移表示（与 JS 字符串索引一致）。 */
export interface TextRange {
  /** 起始 UTF-16 偏移，含。 */
  start: number;
  /** 结束 UTF-16 偏移，不含。 */
  end: number;
}

export interface CodePointRun {
  /** 该码点在原文中的 UTF-16 区间。 */
  range: TextRange;
  /** Unicode 码点数值。 */
  codePoint: number;
  /** 该码点本身。 */
  char: string;
}

/** Unicode 双向格式控制字符（U+200E/U+200F、U+202A..U+202E、U+2066..U+2069）。 */
export const BIDI_CONTROL_CODEPOINTS: readonly number[] = [
  0x200e, 0x200f,
  0x202a, 0x202b, 0x202c, 0x202d, 0x202e,
  0x2066, 0x2067, 0x2068, 0x2069,
];

/** 遍历字符串的码点，并给出每个码点在原文中的 UTF-16 区间。 */
export function codePointRuns(input: string): CodePointRun[] {
  const runs: CodePointRun[] = [];
  for (let index = 0; index < input.length; ) {
    const codePoint = input.codePointAt(index);
    if (codePoint === undefined) {
      break;
    }
    const charSize = codePoint > 0xffff ? 2 : 1;
    runs.push({
      range: { start: index, end: index + charSize },
      codePoint,
      char: String.fromCodePoint(codePoint),
    });
    index += charSize;
  }
  return runs;
}

function countCodePoints(input: string): number {
  let count = 0;
  for (const _char of input) {
    count += 1;
  }
  return count;
}

/**
 * 规范化后的文本，以及规范化串到原串的范围映射。
 */
export class NormalizedText {
  readonly original: string;
  readonly form: NormalizationForm;
  readonly normalized: string;
  readonly runs: CodePointRun[];
  /**
   * boundaries[i] = 原串前 i 个码点规范化结果的 UTF-16 长度。
   * 单调不减；跨码点融合时相邻边界相等（零长度平台）。
   */
  private readonly boundaries: number[];

  constructor(original: string, form: NormalizationForm) {
    this.original = original;
    this.form = form;
    this.runs = codePointRuns(original);

    if (form === "none") {
      this.normalized = original;
      this.boundaries = [0];
      for (const run of this.runs) {
        this.boundaries.push(this.boundaries[this.boundaries.length - 1] + (run.range.end - run.range.start));
      }
      return;
    }

    this.normalized = original.normalize(form);
    this.boundaries = [0];
    let prefix = "";
    for (const run of this.runs) {
      prefix += run.char;
      this.boundaries.push(prefix.normalize(form).length);
    }
  }

  /**
   * 规范化串中的 UTF-16 区间 [start, end) 映射到原文。
   *
   * 头：第一个使累计长度 > start 的前缀边界，确定起点码点；
   * 尾：最后一个累计长度 <= end 的前缀边界（跨过零长度平台），
   * 确保融合所涉及的全部原码点都被覆盖。
   */
  mapNormalizedRangeToOriginal(range: TextRange): TextRange {
    if (this.runs.length === 0) {
      return { start: 0, end: 0 };
    }
    const n = this.runs.length;
    const headBoundary = firstIndexGreaterThan(this.boundaries, range.start);
    const runStart = Math.min(Math.max(headBoundary - 1, 0), n - 1);

    if (range.end === range.start) {
      return {
        start: this.runs[runStart].range.start,
        end: this.runs[runStart].range.end,
      };
    }

    let tailBoundary = lastIndexAtMost(this.boundaries, range.end);
    tailBoundary = Math.min(Math.max(tailBoundary, runStart + 1), n);
    return {
      start: this.runs[runStart].range.start,
      end: this.runs[tailBoundary - 1].range.end,
    };
  }

  /** 规范化串上按字形簇切分，返回每个簇在原串上的覆盖区间。 */
  graphemeRuns(): TextRange[] {
    if (this.normalized.length === 0) {
      return [];
    }
    const segmenter = new Intl.Segmenter([], { granularity: "grapheme" });
    const result: TextRange[] = [];
    for (const segment of segmenter.segment(this.normalized)) {
      result.push(
        this.mapNormalizedRangeToOriginal({
          start: segment.index,
          end: segment.index + segment.segment.length,
        }),
      );
    }
    return result;
  }

  length(unit: LengthUnit): number {
    switch (unit) {
      case "utf16":
        return this.normalized.length;
      case "codepoints":
        return countCodePoints(this.normalized);
      case "graphemes":
        return this.graphemeRuns().length;
    }
  }

  /** 在原文上找出所有双向格式控制字符。 */
  bidiControlsInOriginal(): CodePointRun[] {
    return this.runs.filter((run) => BIDI_CONTROL_CODEPOINTS.includes(run.codePoint));
  }
}

/** 在单调不减数组中找第一个值 > target 的下标；全部 <= 时返回数组长度 - 1。 */
function firstIndexGreaterThan(values: number[], target: number): number {
  let low = 0;
  let high = values.length;
  while (low < high) {
    const mid = Math.floor((low + high) / 2);
    if (values[mid] > target) {
      high = mid;
    } else {
      low = mid + 1;
    }
  }
  return Math.min(low, values.length - 1);
}

/** 在单调不减数组中找最后一个值 <= target 的下标。 */
function lastIndexAtMost(values: number[], target: number): number {
  let low = 0;
  let high = values.length - 1;
  while (low < high) {
    const mid = Math.floor((low + high + 1) / 2);
    if (values[mid] <= target) {
      low = mid;
    } else {
      high = mid - 1;
    }
  }
  return low;
}
