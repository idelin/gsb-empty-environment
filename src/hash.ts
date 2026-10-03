import { createHash } from "node:crypto";

/** 将任意键映射到 32 位无符号整数哈希空间（哈希环上的位置）。 */
export function hashKey(key: string): number {
  const digest = createHash("md5").update(key).digest();
  return digest.readUInt32BE(0);
}
