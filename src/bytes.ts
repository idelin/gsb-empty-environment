/** 带或不带 0x 前缀的十六进制字符串。 */
export type HexString = string;

const HEX_CHARS = /^0x(?:[0-9a-fA-F]*)$/;

/** 去除 0x 前缀。 */
export function stripHexPrefix(value: string): string {
  return value.startsWith("0x") || value.startsWith("0X")
    ? value.slice(2)
    : value;
}

/** 判断字符串是否为合法十六进制（必须带 0x 前缀）。 */
export function isHexString(value: unknown): value is HexString {
  return (
    typeof value === "string" &&
    HEX_CHARS.test(value) &&
    stripHexPrefix(value).length % 2 === 0
  );
}

/** 十六进制字符串转字节数组。 */
export function hexToBytes(value: HexString): Uint8Array {
  if (!isHexString(value)) {
    throw new Error(`invalid hex string: ${String(value)}`);
  }
  const hex = stripHexPrefix(value);
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i += 1) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

/** 字节数组转小写 0x 十六进制字符串。 */
export function bytesToHex(bytes: Uint8Array): HexString {
  let hex = "0x";
  for (const byte of bytes) {
    hex += byte.toString(16).padStart(2, "0");
  }
  return hex;
}

/** 连接多个字节数组，返回新数组。 */
export function concatBytes(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

/** 无符号整数转为定长 32 字节大端表示。 */
export function uintToBytes32(value: bigint): Uint8Array {
  if (value < 0n) {
    throw new Error("negative integer cannot be encoded as uint256");
  }
  if (value >= 2n ** 256n) {
    throw new Error("integer does not fit in uint256");
  }
  const out = new Uint8Array(32);
  let remaining = value;
  for (let i = 31; i >= 0 && remaining > 0n; i -= 1) {
    out[i] = Number(remaining & 0xffn);
    remaining >>= 8n;
  }
  return out;
}

/**
 * 地址归一化：接受 20 字节的 hex（带 0x），返回小写形式用于比较。
 * EIP-55 校验和不做强制要求，只比较底层字节。
 */
export function normalizeAddress(value: string): HexString {
  const bytes = hexToBytes(value);
  if (bytes.length !== 20) {
    throw new Error(`address must be 20 bytes, got ${bytes.length}`);
  }
  return bytesToHex(bytes);
}

/** 以 32 字节大端编码一个地址（左填充 12 个零字节）。 */
export function encodeAddress(value: string): Uint8Array {
  const address = hexToBytes(value);
  if (address.length !== 20) {
    throw new Error(`address must be 20 bytes, got ${address.length}`);
  }
  const out = new Uint8Array(32);
  out.set(address, 12);
  return out;
}

/** 以 UTF-8 编码字符串（用于 string 类型的哈希输入）。 */
export function utf8ToBytes(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

/** 字节数组浅拷贝，避免外部持有的缓冲区被意外修改。 */
export function copyBytes(bytes: Uint8Array): Uint8Array {
  return new Uint8Array(bytes);
}
