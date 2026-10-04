// CRC-32 (IEEE 802.3, polynomial 0xEDB88320), incremental, allocation-free.

const TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  }
  TABLE[n] = c >>> 0;
}

export function crc32Start(): number {
  return 0xffffffff;
}

export function crc32Update(state: number, buf: Uint8Array): number {
  let c = state;
  for (let i = 0; i < buf.length; i++) {
    c = TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  }
  return c >>> 0;
}

export function crc32Digest(state: number): number {
  return (state ^ 0xffffffff) >>> 0;
}

export function crc32(buf: Uint8Array): number {
  return crc32Digest(crc32Update(crc32Start(), buf));
}
