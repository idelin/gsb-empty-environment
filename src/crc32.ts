/**
 * CRC-32 (IEEE 802.3, polynomial 0xEDB88320) — the 4-byte digest primitive
 * exchanged between nodes during liveness probes.
 */

const CRC32_TABLE: Uint32Array = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n += 1) {
    let c = n;
    for (let k = 0; k < 8; k += 1) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  return table;
})();

export function crc32(data: string | Uint8Array): number {
  const bytes = typeof data === "string" ? Buffer.from(data, "utf8") : data;
  let crc = 0xffffffff;
  for (let i = 0; i < bytes.length; i += 1) {
    crc = CRC32_TABLE[(crc ^ bytes[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

/** Render a 4-byte digest as 8 hex chars, e.g. 0xcbf43926 -> "cbf43926". */
export function digestToHex(digest: number): string {
  return (digest >>> 0).toString(16).padStart(8, "0");
}
