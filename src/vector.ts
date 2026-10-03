/**
 * 列式定长数据块（Chunk / Vector）。
 *
 * 每个 Vector 是一块紧凑连续的定长数组（默认 1024 行），
 * 底层为单一 TypedArray 视图，元素紧密排列、无指针间接，
 * 保证 CPU 预取与 SIMD 向量 load/store 友好的内存布局。
 */

export const CHUNK_SIZE = 1024;

export class Int32Vector {
  readonly length: number;
  /** 紧凑连续的数据视图，所有内核只顺序遍历这一段内存 */
  readonly data: Int32Array;

  constructor(length: number = CHUNK_SIZE) {
    this.length = length;
    // V8/Node 的 ArrayBuffer 分配器对大块内存按至少 16 字节（实际 64 字节
    // cache line）对齐分配，Int32Array 视图天然满足 SIMD 对齐要求。
    this.data = new Int32Array(length);
  }

  static from(values: ArrayLike<number>): Int32Vector {
    const vec = new Int32Vector(values.length);
    for (let i = 0; i < values.length; i++) vec.data[i] = values[i];
    return vec;
  }
}
