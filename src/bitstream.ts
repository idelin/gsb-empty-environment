/** 比特刻的 MSB-first 比特流读写器。 */

export class BitWriter {
  private bytes: number[] = [];
  private cur = 0;
  private used = 0;
  /** 已写入的总比特数（用于码率统计） */
  bitsWritten = 0;

  writeBit(bit: 0 | 1): void {
    this.cur = (this.cur << 1) | bit;
    this.used++;
    this.bitsWritten++;
    if (this.used === 8) {
      this.bytes.push(this.cur);
      this.cur = 0;
      this.used = 0;
    }
  }

  finish(): Uint8Array {
    if (this.used > 0) {
      this.bytes.push(this.cur << (8 - this.used));
      this.cur = 0;
      this.used = 0;
    }
    return Uint8Array.from(this.bytes);
  }
}

export class BitReader {
  private pos = 0;
  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  /** 读尽后返回 0 比特（算术解码尾部允许补零） */
  readBit(): 0 | 1 {
    const byte = this.data[this.pos >> 3] ?? 0;
    const bit = (byte >> (7 - (this.pos & 7))) & 1;
    this.pos++;
    return bit as 0 | 1;
  }
}
