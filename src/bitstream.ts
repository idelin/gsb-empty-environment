// 位流写入器：字节内 LSB-first（见 FORMAT.md 第 1 节）。
export class BitWriter {
  private out: number[] = [];
  private cur = 0;
  private nbits = 0;

  writeBit(bit: number): void {
    this.cur |= (bit & 1) << this.nbits;
    this.nbits++;
    if (this.nbits === 8) {
      this.out.push(this.cur);
      this.cur = 0;
      this.nbits = 0;
    }
  }

  // 普通整数：值的 bit 0 先写（LSB-first）。
  writeBits(value: number, count: number): void {
    for (let i = 0; i < count; i++) {
      this.writeBit((value >>> i) & 1);
    }
  }

  // 霍夫曼码字：码长 len、值 code，先写 code 的最高位。
  writeCode(code: number, len: number): void {
    for (let i = len - 1; i >= 0; i--) {
      this.writeBit((code >>> i) & 1);
    }
  }

  // 收尾：不足 8 位补 0，返回字节序列。
  finish(): Uint8Array {
    if (this.nbits > 0) {
      this.out.push(this.cur);
      this.cur = 0;
      this.nbits = 0;
    }
    return Uint8Array.from(this.out);
  }
}
