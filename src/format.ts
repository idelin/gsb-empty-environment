/**
 * 显示值（display）：在 raw 的整数部分插入千位分隔符。
 * 纯函数、无副作用；不解析为 number，因此不会丢失任何原始小数语义
 * （尾点 "12."、前导零 "007"、小数尾零 "0.50"、孤负号 "-" 都原样保留）。
 */

export function formatDisplay(raw: string): string {
  const negative = raw.startsWith("-");
  const body = negative ? raw.slice(1) : raw;
  const dotIndex = body.indexOf(".");
  const intPart = dotIndex === -1 ? body : body.slice(0, dotIndex);
  const fracPart = dotIndex === -1 ? "" : body.slice(dotIndex); // 含 "."
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return (negative ? "-" : "") + grouped + fracPart;
}
