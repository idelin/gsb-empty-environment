// CRC-32 (IEEE 802.3, 多项式 0xEDB88320)，支持增量更新。
const TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  }
  TABLE[n] = c >>> 0;
}

export class Crc32 {
  private crc = 0xffffffff;

  update(data: Uint8Array): void {
    let c = this.crc;
    for (let i = 0; i < data.length; i++) {
      c = TABLE[(c ^ data[i]) & 0xff] ^ (c >>> 8);
    }
    this.crc = c;
  }

  updateByte(b: number): void {
    this.crc = TABLE[(this.crc ^ b) & 0xff] ^ (this.crc >>> 8);
  }

  digest(): number {
    return (this.crc ^ 0xffffffff) >>> 0;
  }
}

export function crc32(data: Uint8Array): number {
  const c = new Crc32();
  c.update(data);
  return c.digest();
}
