const table = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  }
  table[n] = c >>> 0;
}

export function crc32(data: Uint8Array, seed = 0): number {
  let c = seed ^ 0xffffffff;
  for (let i = 0; i < data.length; i++) {
    c = table[(c ^ data[i]) & 0xff] ^ (c >>> 8);
  }
  return (c ^ 0xffffffff) >>> 0;
}
