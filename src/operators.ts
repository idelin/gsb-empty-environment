/**
 * operators.ts —— 向量化算子库。
 *
 * 每个转换算子遵循同一协议：
 *   1. 从 ChunkPool acquire 一个输出 Chunk；
 *   2. 以紧密循环就地计算（无任何对象/数组创建）；
 *   3. 将输入 Chunk release 回池，供下游立即复用；
 *   4. 返回输出 Chunk。
 * 聚合算子消费输入 Chunk，把标量结果写入调用方提供的结果单元（out[0]），
 * 不做标量返回 —— 避免跨函数边界的 double 装箱（HeapNumber 分配）。
 *
 * 算子对象在流水线建立期创建一次，稳态执行期零闭包、零装箱、零分配。
 *
 * 注意（V8 陷阱）：逐元素循环中不要写 `v < lo ? lo : v` 这类
 * "Smi 常量与 Float64 混合的三元表达式"，其 phi 节点会被选择为 Tagged
 * 表示，导致 v 每元素装箱一次。应使用 Math.min/Math.max（见 MapClamp）。
 */

import { CHUNK_SIZE, Chunk, ChunkPool } from "./chunk.ts";

/** 转换算子：Chunk -> Chunk。 */
export interface TransformOp {
  readonly name: string;
  execute(input: Chunk, pool: ChunkPool): Chunk;
}

/** 聚合算子：消费 Chunk，结果写入 out[0]（零装箱协议）。 */
export interface AggregateOp {
  readonly name: string;
  execute(input: Chunk, pool: ChunkPool, out: Float64Array): void;
}

/** 源算子：从池中取 Chunk 并填充数据（物化输入向量）。 */
export interface SourceOp {
  readonly name: string;
  execute(pool: ChunkPool): Chunk;
}

/* ---------------- 源算子 ---------------- */

/** op#1 scan：确定性 LCG 伪随机序列物化，可复现、无需 IO。 */
export class ScanSource implements SourceOp {
  readonly name = "scan(lcg)";
  /**
   * LCG 状态保存在 Uint32Array 单元而非 number 字段：
   * uint32 状态有一半概率超出 Smi 范围，number 字段反复装箱会产生堆分配，
   * 类型化数组单元读写永不装箱（与 JIT 层级无关）。
   */
  private readonly state = new Uint32Array(1);

  constructor(seed: number) {
    this.state[0] = seed >>> 0;
  }

  execute(pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const data = out.data;
    let s = this.state[0];
    for (let i = 0; i < CHUNK_SIZE; i++) {
      // Numerical Recipes LCG 常数
      s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
      data[i] = (s / 4294967296) * 2000 - 1000; // 映射到 [-1000, 1000)
    }
    this.state[0] = s;
    out.length = CHUNK_SIZE;
    return out;
  }
}

/* ---------------- 转换算子 ---------------- */

/** op#2 filter：选择向量压实（保留 >= threshold 的元素）。 */
export class FilterGe implements TransformOp {
  readonly name: string;
  private readonly threshold: number;

  constructor(threshold: number) {
    this.threshold = threshold;
    this.name = `filter(>= ${threshold})`;
  }

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    const t = this.threshold;
    let w = 0;
    for (let i = 0; i < n; i++) {
      const v = src[i];
      if (v >= t) {
        dst[w] = v;
        w++;
      }
    }
    out.length = w;
    pool.release(input);
    return out;
  }
}

/** op#3 map：逐元素加标量。 */
export class MapAdd implements TransformOp {
  readonly name: string;
  private readonly c: number;

  constructor(c: number) {
    this.c = c;
    this.name = `map(+ ${c})`;
  }

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    const c = this.c;
    for (let i = 0; i < n; i++) dst[i] = src[i] + c;
    out.length = n;
    pool.release(input);
    return out;
  }
}

/** op#4 map：逐元素乘标量。 */
export class MapMul implements TransformOp {
  readonly name: string;
  private readonly c: number;

  constructor(c: number) {
    this.c = c;
    this.name = `map(* ${c})`;
  }

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    const c = this.c;
    for (let i = 0; i < n; i++) dst[i] = src[i] * c;
    out.length = n;
    pool.release(input);
    return out;
  }
}

/** op#5 map：平方根（对负数取 |v| 的平方根，保证定义域安全）。 */
export class MapSqrt implements TransformOp {
  readonly name = "map(sqrt(|v|))";

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    for (let i = 0; i < n; i++) dst[i] = Math.sqrt(Math.abs(src[i]));
    out.length = n;
    pool.release(input);
    return out;
  }
}

/** op#6 map：绝对值。 */
export class MapAbs implements TransformOp {
  readonly name = "map(abs)";

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    for (let i = 0; i < n; i++) dst[i] = Math.abs(src[i]);
    out.length = n;
    pool.release(input);
    return out;
  }
}

/** op#7 map：区间截断 clamp。 */
export class MapClamp implements TransformOp {
  readonly name: string;
  private readonly lo: number;
  private readonly hi: number;

  constructor(lo: number, hi: number) {
    this.lo = lo;
    this.hi = hi;
    this.name = `clamp(${lo}, ${hi})`;
  }

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    const lo = this.lo;
    const hi = this.hi;
    // 必须写成 Math.min/max：等价三元表达式会产生 Smi/Float64 混合 phi，
    // 被 V8 选择为 Tagged 表示，导致每元素装箱一次堆分配。
    for (let i = 0; i < n; i++) dst[i] = Math.min(hi, Math.max(lo, src[i]));
    out.length = n;
    pool.release(input);
    return out;
  }
}

/** op#8 map：三角函数 sin。 */
export class MapSin implements TransformOp {
  readonly name = "map(sin)";

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    for (let i = 0; i < n; i++) dst[i] = Math.sin(src[i]);
    out.length = n;
    pool.release(input);
    return out;
  }
}

/** op#9 map：取负。 */
export class MapNegate implements TransformOp {
  readonly name = "map(negate)";

  execute(input: Chunk, pool: ChunkPool): Chunk {
    const out = pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    for (let i = 0; i < n; i++) dst[i] = -src[i];
    out.length = n;
    pool.release(input);
    return out;
  }
}

/* ---------------- 聚合算子 ---------------- */

/** op#10 aggregate：全量求和，结果写入 out[0]，消费输入并归还 Chunk。 */
export class AggSum implements AggregateOp {
  readonly name = "agg(sum)";

  execute(input: Chunk, pool: ChunkPool, out: Float64Array): void {
    const src = input.data;
    const n = input.length;
    let acc = 0;
    for (let i = 0; i < n; i++) acc += src[i];
    pool.release(input);
    out[0] = acc;
  }
}
