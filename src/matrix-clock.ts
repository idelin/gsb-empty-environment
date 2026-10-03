/**
 * 矩阵时钟（Matrix Clock）
 *
 * M[i][j] 表示：本节点所认知的「节点 i 所认知的节点 j 的逻辑时钟」。
 *  - 第 self 行即本节点自己的向量时钟；
 *  - 第 i 行（i ≠ self）是本节点最近一次从节点 i 收到的、节点 i 的向量时钟认知。
 *
 * 垃圾回收关键性质：
 *  对每一列 j 取所有行的最小值 min_i M[i][j]，得到「集群中每个节点都已确认知悉
 *  节点 j 至少推进到该时刻」。任何向量时间戳被该 min 向量逐分量支配（<=）的事件，
 *  都已被全网每一个节点接收并处理，其日志可安全物理截断。
 */

export type Vector = number[];

export class MatrixClock {
  readonly nodeId: number;
  readonly size: number;
  private readonly m: number[][];

  constructor(nodeId: number, size: number) {
    if (nodeId < 0 || nodeId >= size) {
      throw new RangeError(`nodeId ${nodeId} out of range [0, ${size})`);
    }
    this.nodeId = nodeId;
    this.size = size;
    this.m = Array.from({ length: size }, () => new Array<number>(size).fill(0));
  }

  get(row: number, col: number): number {
    return this.m[row][col];
  }

  /** 本节点自己的向量时钟（矩阵第 self 行的拷贝）。 */
  ownVector(): Vector {
    return [...this.m[this.nodeId]];
  }

  /** 整个矩阵的深拷贝（用于随消息传播）。 */
  matrix(): number[][] {
    return this.m.map((row) => [...row]);
  }

  /** 本地事件：推进自己的时钟分量，返回事件时间戳。 */
  tick(): Vector {
    this.m[this.nodeId][this.nodeId] += 1;
    return this.ownVector();
  }

  /**
   * 接收事件：合并发送方 k 携带的矩阵快照。
   *  1) 用发送方自己的向量（第 k 行）逐分量 max 合并进自己的向量；
   *  2) 其余各行（i ≠ self）也逐分量 max 合并（间接认知传播）；
   *  3) 自己的时钟分量 +1（接收本身也是一个事件）。
   */
  receive(senderId: number, senderMatrix: number[][]): Vector {
    for (let j = 0; j < this.size; j++) {
      this.m[this.nodeId][j] = Math.max(this.m[this.nodeId][j], senderMatrix[senderId][j]);
    }
    for (let i = 0; i < this.size; i++) {
      if (i === this.nodeId) continue;
      for (let j = 0; j < this.size; j++) {
        this.m[i][j] = Math.max(this.m[i][j], senderMatrix[i][j]);
      }
    }
    this.m[this.nodeId][this.nodeId] += 1;
    return this.ownVector();
  }

  /**
   * GC 安全水位：逐列取最小值。
   * horizon[j] = min_i M[i][j]，即「每个节点都已知悉节点 j 至少推进到 horizon[j]」。
   */
  horizon(): Vector {
    const h = new Array<number>(this.size).fill(Number.POSITIVE_INFINITY);
    for (let i = 0; i < this.size; i++) {
      for (let j = 0; j < this.size; j++) {
        h[j] = Math.min(h[j], this.m[i][j]);
      }
    }
    return h;
  }

  /** 事件时间戳 v 是否已被全网知悉（被 GC 水位逐分量支配）。 */
  static coveredBy(v: Vector, horizon: Vector): boolean {
    return v.every((value, j) => value <= horizon[j]);
  }

  toString(): string {
    const header = '      ' + Array.from({ length: this.size }, (_, j) => `k${j}`).join(' ');
    const rows = this.m.map(
      (row, i) => `  n${i} [ ${row.map((v) => String(v).padStart(2)).join(' ')} ]`,
    );
    return [header, ...rows].join('\n');
  }
}
