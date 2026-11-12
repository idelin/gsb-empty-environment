import { domainToASCII } from "node:url";

/**
 * 把域名归一化成小写 Punycode（A-label）。
 * - Unicode（U-label）转 A-label：bücher.example -> xn--bcher-kva.example
 * - 已是 Punycode 的大小写混合输入（XN--BCHER...）也归一到同一小写形式，
 *   因此 IDN 大小写差异不会造成绕过。
 * - 去掉末尾根点、全部转小写。
 * 非法域名返回 null（fail-closed）。
 */
export function normalizeDomain(input: string): string | null {
  if (typeof input !== "string") {
    return null;
  }
  const trimmed = input.trim().replace(/\.$/, "");
  if (trimmed.length === 0) {
    return null;
  }
  let ascii: string;
  try {
    ascii = domainToASCII(trimmed);
  } catch {
    return null;
  }
  if (!ascii || ascii.length > 253) {
    return null;
  }
  // domainToASCII 对含非法字符的输入可能返回空串；再做一次标签校验。
  const labels = ascii.split(".");
  for (const label of labels) {
    if (label.length === 0 || label.length > 63 || /[^a-z0-9-]/.test(label) ||
      label.startsWith("-") || label.endsWith("-")) {
      return null;
    }
  }
  return ascii.toLowerCase();
}

/** 按小写 Punycode 比较两个域名是否同一对象。任一非法即不相等。 */
export function sameDomain(a: string, b: string): boolean {
  const na = normalizeDomain(a);
  const nb = normalizeDomain(b);
  return na !== null && nb !== null && na === nb;
}
