/**
 * 内存缓冲区池：为每条待持久化日志分配一个缓冲槽位，
 * 落盘或回滚时释放。守恒不变量：totalAcquired === totalReleased + outstanding。
 */
export class BufferPool {
  private live = new Set<number>();
  private nextId = 1;
  private acquired = 0;
  private released = 0;

  acquire(): number {
    const id = this.nextId++;
    this.live.add(id);
    this.acquired++;
    return id;
  }

  release(id: number): void {
    if (!this.live.delete(id)) {
      throw new Error(`buffer ${id} double-release or unknown`);
    }
    this.released++;
  }

  get outstanding(): number {
    return this.live.size;
  }

  get totalAcquired(): number {
    return this.acquired;
  }

  get totalReleased(): number {
    return this.released;
  }
}
