// 固定大小的 RGBA float32 瓦片缓冲池，带分配统计。
export class BufferPool {
  private freeList: Float32Array[] = [];
  allocations = 0; // 实际 new 的次数
  reuses = 0;      // 从空闲链表复用的次数
  live = 0;        // 当前借出未归还的缓冲数

  readonly floatsPerBuffer: number;

  constructor(floatsPerBuffer: number) {
    this.floatsPerBuffer = floatsPerBuffer;
  }

  acquire(): Float32Array {
    const buf = this.freeList.pop();
    if (buf) {
      this.reuses++;
      this.live++;
      return buf;
    }
    this.allocations++;
    this.live++;
    return new Float32Array(this.floatsPerBuffer);
  }

  release(buf: Float32Array): void {
    this.live--;
    this.freeList.push(buf);
  }
}
