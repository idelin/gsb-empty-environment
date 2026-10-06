/**
 * 原始值（raw）净化：保证任意时刻 raw 都满足金额语法。
 *
 * 合法 raw 的语法（允许中间态）：
 *   raw := "-"? body
 *   body := 数字串，至多包含一个 "."
 * 例如：""、"-"、"12."、".5"、"-0.50"、"007" 均为合法中间态，
 * 库不会把它们"修正"成 0 或其它规范形 —— 原始小数语义完全保留。
 */

export interface SanitizeResult {
  /** 净化后的合法 raw */
  raw: string;
  /**
   * 净化后光标在 raw 坐标系中的位置（字符边界下标，0..raw.length）。
   * 由 candidateCaret 换算：统计候选串中位于光标边界之前且被保留的字符数。
   */
  caret: number;
}

/**
 * 净化候选串，同时把候选串坐标系中的光标边界映射到净化结果坐标系。
 * 规则：
 *  - 数字：总是保留；
 *  - "."：仅保留第一个；
 *  - "-"：仅当净化结果当前为空（即它会成为首字符）时保留；
 *  - 其余字符（含千位逗号、空白、字母等）：丢弃。
 */
export function sanitizeWithCaret(candidate: string, candidateCaret: number): SanitizeResult {
  let raw = "";
  let caret = 0;
  let seenDot = false;
  for (let i = 0; i < candidate.length; i++) {
    const ch = candidate[i];
    let keep = false;
    if (ch >= "0" && ch <= "9") {
      keep = true;
    } else if (ch === ".") {
      if (!seenDot) {
        keep = true;
        seenDot = true;
      }
    } else if (ch === "-") {
      if (raw.length === 0) {
        keep = true;
      }
    }
    if (keep) {
      raw += ch;
      if (i < candidateCaret) caret++;
    }
  }
  return { raw, caret };
}

/** 净化任意输入为合法 raw（光标置于末尾）。 */
export function sanitizeRaw(input: string): string {
  return sanitizeWithCaret(input, input.length).raw;
}

/** 判断字符串是否已是合法 raw。 */
export function isValidRaw(raw: string): boolean {
  return /^-?\d*(\.\d*)?$/.test(raw);
}
