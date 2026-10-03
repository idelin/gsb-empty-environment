/**
 * 矩阵时钟（Matrix Clock）。
 *
 * matrix[r][c] 表示：本节点所认知的「节点 r 已确认知悉来自节点 c 的事件，
 * 直到逻辑时间 matrix[r][c]（含）」。
 *
 * - 第 self 行即本节点自己的向量时钟；
 * - 其余行是本节点通过消息交换学到的、其他节点的向量时钟认知。
 *
 * 垃圾回收水位线：对事件源 c，gcWatermark(c) = min_r(matrix[r][c])，
 * 即集群中「所有节点都已确认知悉」的最大 LSN。LSN <= 水位线的历史日志
 * 可以安全地进行物理截断。
 */

export type MatrixSnapshot = number[][];

export class MatrixClock {
  readonly nodeIds: string[];
  readonly selfIndex: number;
  private matrix: number[][];

  constructor(nodeIds: string[], selfId: string) {
    if (nodeIds.length === 0) throw new Error("cluster must not be empty");
    this.nodeIds = [...nodeIds];
    this.selfIndex = this.nodeIds.indexOf(selfId);
    if (this.selfIndex < 0) {
      throw new Error(`node "${selfId}" is not in cluster [${nodeIds}]`);
    }
    this.matrix = this.nodeIds.map(() => this.nodeIds.map(() => 0));
  }

  get selfId(): string {
    return this.nodeIds[this.selfIndex];
  }

  /** 本地事件发生：推进自身向量时钟的自身分量，返回新的逻辑时间。 */
  tick(): number {
    this.matrix[this.selfIndex][this.selfIndex] += 1;
    return this.matrix[this.selfIndex][this.selfIndex];
  }

  /** 本地确认：已接收并处理来自 origin 的事件直到 lsn（含）。 */
  acknowledge(originId: string, lsn: number): void {
    const origin = this.indexOf(originId);
    const row = this.matrix[this.selfIndex];
    if (row[origin] < lsn) row[origin] = lsn;
  }

  /** 生成矩阵快照，随消息发送给对端。 */
  snapshot(): MatrixSnapshot {
    return this.matrix.map((row) => [...row]);
  }

  /** 合并对端随消息携带的矩阵：逐行逐列取最大值。 */
  merge(remote: MatrixSnapshot): void {
    for (let r = 0; r < this.nodeIds.length; r++) {
      for (let c = 0; c < this.nodeIds.length; c++) {
        if (remote[r] !== undefined && remote[r][c] > this.matrix[r][c]) {
          this.matrix[r][c] = remote[r][c];
        }
      }
    }
  }

  /**
   * 垃圾回收水位线：集群中每个节点都已确认知悉 origin 的事件，
   * 直到返回的 LSN（含）。等于矩阵第 origin 列的逐行最小值。
   */
  gcWatermark(originId: string): number {
    const origin = this.indexOf(originId);
    let min = Infinity;
    for (let r = 0; r < this.nodeIds.length; r++) {
      if (this.matrix[r][origin] < min) min = this.matrix[r][origin];
    }
    return min;
  }

  valueAt(rowId: string, colId: string): number {
    return this.matrix[this.indexOf(rowId)][this.indexOf(colId)];
  }

  private indexOf(id: string): number {
    const i = this.nodeIds.indexOf(id);
    if (i < 0) throw new Error(`unknown node id: "${id}"`);
    return i;
  }

  /** 便于演示打印的格式化矩阵。 */
  format(): string {
    const header = "      " + this.nodeIds.map((id) => id.padStart(4)).join("");
    const rows = this.nodeIds.map((id, r) => {
      const cells = this.matrix[r].map((v) => String(v).padStart(4)).join("");
      return `  ${id}  ${cells}`;
    });
    return [header, ...rows].join("\n");
  }
}
