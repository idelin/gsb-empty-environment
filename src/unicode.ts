/**
 * 按 Unicode 码点（code point）计数的字符串工具。
 * 注意：码点不等于用户感知字符（grapheme cluster），
 * 例如 "🇨🇳" 是 2 个码点、"👨‍👩‍👧" 是多个码点加 ZWJ。
 * 本模块严格按需求以码点为单位计数与截断，不会拆散代理对。
 */

/** 返回字符串的 Unicode 码点数量（"😀" 计为 1，而非 UTF-16 的 2）。 */
export function codePointLength(text: string): number {
  let count = 0;
  for (const _ of text) {
    count += 1;
  }
  return count;
}

/** 截断到最多 max 个码点，保证不拆散代理对。 */
export function truncateToCodePoints(text: string, max: number): string {
  if (!Number.isInteger(max) || max < 0) {
    throw new RangeError(`max 必须是非负整数，收到: ${max}`);
  }
  let count = 0;
  let utf16Index = 0;
  for (const char of text) {
    if (count === max) {
      break;
    }
    count += 1;
    utf16Index += char.length;
  }
  return text.slice(0, utf16Index);
}

/** 把码点偏移量夹取到 [0, codePointLength(text)] 区间。 */
export function clampCodePointOffset(text: string, offset: number): number {
  if (offset < 0) {
    return 0;
  }
  const length = codePointLength(text);
  return offset > length ? length : offset;
}

/** 按码点偏移切片（等价于 [...text].slice(start, end).join("")）。 */
export function sliceByCodePoints(text: string, start: number, end?: number): string {
  return [...text].slice(start, end).join("");
}
