/**
 * Slice —— 环形内存池中一块只读数据的零拷贝引用。
 *
 * Slice 本身不持有任何数据拷贝，仅记录 (buffer, offset, length)。
 * 通过引用计数跟踪生命周期：计数归零时通知内存池回收底层块，
 * 并将 Slice 对象本身交还对象池复用，从而将 GC 压力降到接近零。
 */

export interface SliceOwner {
  retainBlock(blockIndex: number): void;
  releaseBlock(blockIndex: number): void;
  recycleSlice(slice: Slice): void;
}

export class Slice {
  private owner: SliceOwner | null = null;
  private buf: ArrayBuffer | null = null;
  private blockIndex = -1;
  private offset = 0;
  private len = 0;
  private refs = 0;
  private cachedView: Uint8Array | null = null;

  /** 由 RingBufferPool 在借出前调用，重置对象状态（对象池复用）。 */
  _init(
    owner: SliceOwner,
    buf: ArrayBuffer,
    blockIndex: number,
    offset: number,
    length: number,
  ): void {
    this.owner = owner;
    this.buf = buf;
    this.blockIndex = blockIndex;
    this.offset = offset;
    this.len = length;
    this.refs = 1;
    this.cachedView = null;
  }

  get length(): number {
    return this.len;
  }

  /** 当前引用计数（>=1 表示存活，0 表示已释放）。 */
  get refCount(): number {
    return this.refs;
  }

  get released(): boolean {
    return this.refs === 0;
  }

  /** 底层共享缓冲（用于验证零拷贝：与 pool.buffer 为同一对象）。 */
  get buffer(): ArrayBuffer {
    this.assertAlive();
    return this.buf!;
  }

  /** 只读视图（懒创建并缓存）。调用方不应修改其中内容。 */
  get view(): Uint8Array {
    this.assertAlive();
    if (this.cachedView === null) {
      this.cachedView = new Uint8Array(this.buf!, this.offset, this.len);
    }
    return this.cachedView;
  }

  at(index: number): number {
    this.assertAlive();
    if (index < 0 || index >= this.len) {
      throw new RangeError(`index ${index} out of bounds (length ${this.len})`);
    }
    return this.view[index];
  }

  /** 显式拷贝出口：确实需要物化数据时使用。 */
  copyTo(target: Uint8Array, targetOffset = 0): void {
    this.assertAlive();
    target.set(this.view, targetOffset);
  }

  /** 增加一次引用（如把切片转交给另一个消费者）。 */
  retain(): Slice {
    this.assertAlive();
    this.refs++;
    this.owner!.retainBlock(this.blockIndex);
    return this;
  }

  /** 释放一次引用；归零时回收底层块并把自身交还对象池。 */
  release(): void {
    if (this.refs === 0) {
      throw new Error("slice released more times than retained");
    }
    this.refs--;
    this.owner!.releaseBlock(this.blockIndex);
    if (this.refs === 0) {
      const owner = this.owner!;
      this.owner = null;
      this.buf = null;
      this.blockIndex = -1;
      this.offset = 0;
      this.len = 0;
      this.cachedView = null;
      owner.recycleSlice(this);
    }
  }

  /** 支持 `using` 语法（显式资源管理）。 */
  [Symbol.dispose](): void {
    if (!this.released) this.release();
  }

  private assertAlive(): void {
    if (this.refs === 0) {
      throw new Error("slice has been released");
    }
  }
}
