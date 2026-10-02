import { createHash } from "node:crypto";

/**
 * 将任意字符串映射到 32 位无符号哈希空间 [0, 2^32)。
 * 使用 MD5 的前 4 个字节，分布均匀且确定性，适合一致性哈希场景。
 */
export function hash32(input: string): number {
  const digest = createHash("md5").update(input).digest();
  return digest.readUInt32BE(0);
}

/** 哈希空间大小（2^32），用于计算弧段占比。 */
export const HASH_SPACE = 0x1_0000_0000;
