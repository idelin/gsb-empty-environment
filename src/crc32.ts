// CRC-32 (IEEE 802.3, polynomial 0xEDB88320), table-driven.

const TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  }
  TABLE[n] = c >>> 0;
}

export function crc32(buf: Uint8Array, start = 0, end: number = buf.length): number {
  let c = 0xffffffff;
  for (let i = start; i < end; i++) {
    c = TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  }
  return (c ^ 0xffffffff) >>> 0;
}
