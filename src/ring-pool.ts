/**
 * 定长环形内存池（RingPool）+ 零拷贝切片复用器（Slice）。
 *
 * 设计要点：
 * - 常驻内存仅为一块固定大小的 `Buffer`（环形空间），进程生命周期内不扩容、不释放。
 * - 生产者通过 `writeFrame` 追加帧；消费者通过 `readFrame` 切出只读切片（Slice）。
 *   切片仅持有「环形区偏移 + 长度」的引用，不发生任何内存拷贝。
 * - 切片引用计数归零后被安全回收：对象本身进入对象池复用（GC 压力趋近于零），
 *   其钉住的环形区间按 FIFO 水位（pinPos）释放，供写入方覆盖复用。
 * - 为保证切片物理连续，写入时若帧会跨越环形末尾，先在环尾插入 padding
 *   （padding 有界：单帧最大长度），帧数据因此永远一次性连续拷贝进环。
 * - 背压：环形空间不足时 `writeFrame` 返回 false，由调用方决定等待或丢弃。
 */

/** 帧描述符（对象池复用，避免每帧分配）。 */
class FrameNode {
  pos = 0; // 帧在逻辑流中的起始位置
  len = 0;
  next: FrameNode | null = null;
}

/** 钉住节点：记录一个已切出但未释放的区间（对象池复用）。 */
class PinNode {
  pos = 0; // 切片在逻辑流中的起始位置
  released = false;
  prev: PinNode | null = null;
  next: PinNode | null = null;
}

/**
 * 只读切片：对环形内存区间 [offset, offset + byteLength) 的零拷贝引用。
 *
 * 生命周期：由 `RingPool.readFrame()` 借出，初始引用计数为 1；
 * `retain()` 增加计数，`release()` 减少计数，归零时切片失效并归还对象池。
 * 释放后再访问会抛错。切片对象本身会被池化复用，请勿保存释放后的引用。
 */
export class Slice {
  private owner: RingPool | null = null;
  private arena: Buffer | null = null;
  private offset = 0;
  private len = 0;
  private refs = 0;
  private pin: PinNode | null = null;

  /** 切片长度（字节）。 */
  get byteLength(): number {
    return this.len;
  }

  /** 是否已释放。 */
  get released(): boolean {
    return this.owner === null;
  }

  /** @internal 仅 RingPool 调用：绑定到一段环形区间。 */
  _attach(owner: RingPool, arena: Buffer, offset: number, len: number, pin: PinNode): void {
    this.owner = owner;
    this.arena = arena;
    this.offset = offset;
    this.len = len;
    this.refs = 1;
    this.pin = pin;
  }

  private assertAlive(): void {
    if (this.owner === null || this.arena === null) {
      throw new Error('Slice has been released');
    }
  }

  /** 增加引用计数（用于把切片转交给其他消费者）。 */
  retain(): this {
    this.assertAlive();
    this.refs++;
    return this;
  }

  /** 减少引用计数；归零时回收切片并释放其钉住的环形区间。 */
  release(): void {
    this.assertAlive();
    if (--this.refs === 0) {
      const owner = this.owner;
      const pin = this.pin as PinNode;
      this.owner = null;
      this.arena = null;
      this.pin = null;
      this.len = 0;
      owner._releaseSlice(this, pin);
    }
  }

  /** 零分配读取单个字节。 */
  get(index: number): number {
    this.assertAlive();
    if (index < 0 || index >= this.len) {
      throw new RangeError(`index ${index} out of bounds (length ${this.len})`);
    }
    return (this.arena as Buffer)[this.offset + index];
  }

  /**
   * 零拷贝视图：与环形内存共享同一块物理内存，不复制数据。
   * 注意：视图为只读语义，调用方不得写入（写入会污染池中数据）。
   * 每次调用会创建一个轻量视图对象；热路径请使用 `get()` 或 `copyTo()`。
   */
  view(): Buffer {
    this.assertAlive();
    return (this.arena as Buffer).subarray(this.offset, this.offset + this.len);
  }

  /** 将切片内容拷贝到目标缓冲，返回拷贝的字节数。 */
  copyTo(target: Uint8Array, targetOffset = 0): number {
    this.assertAlive();
    if (target.length - targetOffset < this.len) {
      throw new RangeError('target buffer too small');
    }
    target.set((this.arena as Buffer).subarray(this.offset, this.offset + this.len), targetOffset);
    return this.len;
  }

  /** 拷贝出一份独立拥有的 Buffer（需要长期持有时使用）。 */
  toBuffer(): Buffer {
    return Buffer.from(this.view());
  }
}

export class RingPool {
  /** 环形空间容量（字节）。 */
  readonly capacity: number;
  /** 常驻环形内存（唯一的大块分配）。标记为只读引用，请勿直接写入。 */
  readonly arena: Buffer;

  // 逻辑位置（单调递增，物理偏移 = pos % capacity）
  private writePos = 0; // 下一个可写位置
  private readPos = 0;  // 下一个未读帧之后的位置
  private pinPos = 0;   // 回收水位：低于此位置的内存均可被覆盖

  // 待读帧队列（FIFO，节点池化）
  private frameHead: FrameNode | null = null;
  private frameTail: FrameNode | null = null;
  private frameNodePool: FrameNode[] = [];

  // 钉住区间链表（按切出顺序，节点池化）
  private pinHead: PinNode | null = null;
  private pinTail: PinNode | null = null;
  private pinNodePool: PinNode[] = [];

  // Slice 对象池
  private slicePool: Slice[] = [];

  // ---- 运行统计（用于守恒校验与 GC 开销观测）----
  framesWritten = 0;
  framesRead = 0;
  bytesWritten = 0;
  bytesRead = 0;
  paddingBytes = 0;
  /** Slice 对象真实分配次数（稳态下应远小于帧数，证明对象池复用）。 */
  slicesAllocated = 0;
  slicesReleased = 0;

  constructor(capacity: number) {
    if (!Number.isInteger(capacity) || capacity <= 0) {
      throw new RangeError('capacity must be a positive integer');
    }
    this.capacity = capacity;
    this.arena = Buffer.allocUnsafe(capacity);
  }

  /** 已占用（不可覆盖）的环形空间字节数。 */
  get used(): number {
    return this.writePos - this.pinPos;
  }

  /** 剩余可写字节数。 */
  get free(): number {
    return this.capacity - this.used;
  }

  /** 当前在途（借出未释放）的切片数。 */
  get activeSlices(): number {
    return this.framesRead - this.slicesReleased;
  }

  /** 已写入但尚未读出的帧数。 */
  get pendingFrames(): number {
    return this.framesWritten - this.framesRead;
  }

  /**
   * 向环形流追加一帧。帧数据被一次性连续拷入环内（必要时先在环尾插入 padding）。
   * @returns 成功返回 true；空间不足（背压）返回 false，调用方应消费后重试。
   */
  writeFrame(data: Uint8Array): boolean {
    const len = data.length;
    if (len === 0) {
      throw new RangeError('frame must not be empty');
    }
    if (len > this.capacity) {
      throw new RangeError(`frame (${len}B) exceeds ring capacity (${this.capacity}B)`);
    }

    const phys = this.writePos % this.capacity;
    const pad = phys + len > this.capacity ? this.capacity - phys : 0;
    if (this.free < pad + len) {
      return false; // 背压：空间不足，不破坏已有数据
    }

    if (pad > 0) {
      this.writePos += pad;
      this.paddingBytes += pad;
    }
    const offset = this.writePos % this.capacity;
    this.arena.set(data, offset); // 唯一的拷贝点：生产者数据进入环形区
    this.writePos += len;

    const node = this.frameNodePool.pop() ?? new FrameNode();
    node.pos = this.writePos - len;
    node.len = len;
    node.next = null;
    if (this.frameTail !== null) {
      this.frameTail.next = node;
    } else {
      this.frameHead = node;
    }
    this.frameTail = node;

    this.framesWritten++;
    this.bytesWritten += len;
    return true;
  }

  /**
   * 从流中切出下一帧的只读切片（零拷贝）。无完整帧时返回 null。
   * 切片钉住其引用的环形区间，直到引用计数归零才被覆盖复用。
   */
  readFrame(): Slice | null {
    const frame = this.frameHead;
    if (frame === null) {
      return null;
    }
    this.frameHead = frame.next;
    if (this.frameHead === null) {
      this.frameTail = null;
    }
    const pos = frame.pos;
    const len = frame.len;
    frame.next = null;
    this.frameNodePool.push(frame);

    this.readPos = pos + len;
    this.framesRead++;
    this.bytesRead += len;

    const pin = this.pinNodePool.pop() ?? new PinNode();
    pin.pos = pos;
    pin.released = false;
    pin.prev = this.pinTail;
    pin.next = null;
    if (this.pinTail !== null) {
      this.pinTail.next = pin;
    } else {
      this.pinHead = pin;
    }
    this.pinTail = pin;

    let slice = this.slicePool.pop();
    if (slice === undefined) {
      slice = new Slice();
      this.slicesAllocated++;
    }
    slice._attach(this, this.arena, pos % this.capacity, len, pin);
    return slice;
  }

  /** @internal 仅 Slice 调用：引用计数归零后的回收。 */
  _releaseSlice(slice: Slice, pin: PinNode): void {
    this.slicesReleased++;
    pin.released = true;
    // 从队首起推进回收水位：乱序释放的区间要等最老切片释放后才可覆盖。
    while (this.pinHead !== null && this.pinHead.released) {
      const dead = this.pinHead;
      this.pinHead = dead.next;
      if (this.pinHead !== null) {
        this.pinHead.prev = null;
      } else {
        this.pinTail = null;
      }
      dead.prev = null;
      dead.next = null;
      dead.released = false;
      this.pinNodePool.push(dead);
    }
    this.pinPos = this.pinHead !== null ? this.pinHead.pos : this.readPos;
    this.slicePool.push(slice);
  }
}
