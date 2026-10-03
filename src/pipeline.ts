/**
 * pipeline.ts —— 向量化查询流水线。
 *
 * 建立期（允许分配）：创建算子链、预分配 ChunkPool 与结果单元。
 * 执行期（零分配）：runOnce 只做固定下标循环与 Chunk 借还，
 * 聚合结果写入预分配的 result 单元而非标量返回，
 * 不创建任何对象、数组、闭包，堆分配计数保持为零。
 */

import { Chunk, ChunkPool } from "./chunk.ts";
import type { AggregateOp, SourceOp, TransformOp } from "./operators.ts";

export class Pipeline {
  private readonly pool: ChunkPool;
  private readonly source: SourceOp;
  private readonly transforms: TransformOp[];
  private readonly sink: AggregateOp;
  private readonly stageCount: number;

  /**
   * 聚合结果单元（建立期预分配）。
   * runOnce 执行完毕后，结果位于 result[0]。
   * 通过单元传递标量而非函数返回值，消除跨函数边界的 double 装箱。
   */
  readonly result: Float64Array = new Float64Array(1);

  constructor(
    pool: ChunkPool,
    source: SourceOp,
    transforms: TransformOp[],
    sink: AggregateOp,
  ) {
    this.pool = pool;
    this.source = source;
    this.transforms = transforms;
    this.sink = sink;
    this.stageCount = 1 + transforms.length + 1;
  }

  /** 流水线算子总数（源 + 转换 + 聚合）。 */
  get operatorCount(): number {
    return this.stageCount;
  }

  get chunkPool(): ChunkPool {
    return this.pool;
  }

  /**
   * 执行一次完整流水线，聚合结果写入 this.result[0]。
   * 热路径：除局部基本类型外无任何堆分配。
   */
  runOnce(): void {
    let chunk: Chunk = this.source.execute(this.pool);
    const ops = this.transforms;
    for (let i = 0; i < ops.length; i++) {
      chunk = ops[i].execute(chunk, this.pool);
    }
    this.sink.execute(chunk, this.pool, this.result);
  }

  /**
   * 便捷接口：执行一次并以 number 返回聚合结果。
   * 仅供非热路径（如测试断言）使用 —— 标量返回本身会产生一次装箱。
   */
  runOnceScalar(): number {
    this.runOnce();
    return this.result[0];
  }

  /** 连续执行 iterations 次，结果保留在 this.result[0]。 */
  run(iterations: number): void {
    for (let i = 0; i < iterations; i++) {
      this.runOnce();
    }
  }
}
