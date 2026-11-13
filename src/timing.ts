/**
 * 常数时间比较原语，用于密钥材料比对，防止通过响应耗时逐字节推断。
 * 注意：长度不同会提前返回，长度本身通常不具机密性（UAK 固定 16 字节）。
 */
export function timingSafeEqualBytes(
  left: Uint8Array,
  right: Uint8Array,
): boolean {
  if (left.length !== right.length) {
    return false;
  }
  let diff = 0;
  for (let index = 0; index < left.length; index += 1) {
    diff |= left[index] ^ right[index];
  }
  return diff === 0;
}

/** 字符串的常数时间比较，用于发送者标识等文本字段。 */
export function timingSafeEqualString(left: string, right: string): boolean {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  return timingSafeEqualBytes(leftBytes, rightBytes);
}
