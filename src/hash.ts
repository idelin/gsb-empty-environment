import { createHash } from "node:crypto";

/** Hash an arbitrary string to a uint32 position on the ring (md5, first 4 bytes). */
export function hashKey(key: string): number {
  const digest = createHash("md5").update(key).digest();
  return digest.readUInt32BE(0);
}
