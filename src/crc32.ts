// CRC-32 (IEEE 802.3, polynomial 0xEDB88320), table-driven.
const table = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  }
  table[n] = c >>> 0;
}

export function crc32(input: string | Uint8Array): number {
  const bytes = typeof input === 'string' ? new TextEncoder().encode(input) : input;
  let crc = 0xffffffff;
  for (let i = 0; i < bytes.length; i++) {
    crc = table[(crc ^ bytes[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

export function crc32hex(input: string | Uint8Array): string {
  return crc32(input).toString(16).padStart(8, '0');
}
