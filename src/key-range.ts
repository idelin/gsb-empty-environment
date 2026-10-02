/**
 * 键区间 [start, end)：按 key 的首字母分桶。
 *
 * 语义：首字母 >= start 且首字母 <= end（end 为区间上界字母，闭端；
 * 记法沿用规范的 [start-end) 写法）。边界字母同时落在相邻两区间的
 * contains 范围内时，由路由器按“最后一个 start <= key 的分区”确定性
 * 地归属到右侧分区，因此 'M' 开头的 key 归属 [M-Z)。
 * end 为 null 表示开放上界（正无穷）。
 */
export class KeyRange {
  readonly start: string;
  readonly end: string | null;

  constructor(start: string, end: string | null) {
    if (start.length === 0) throw new Error("start 不能为空");
    if (end !== null && start >= end) {
      throw new Error(`非法区间: [${start}, ${end}) 为空或倒置`);
    }
    this.start = start;
    this.end = end;
  }

  contains(key: string): boolean {
    if (key.length === 0) return false;
    const head = key[0];
    return (
      head >= this.start[0] && (this.end === null || head <= this.end[0])
    );
  }

  /** splitKey 的首字母必须严格落在区间内部才能分裂 */
  canSplitAt(splitKey: string): boolean {
    if (splitKey.length === 0) return false;
    const head = splitKey[0];
    return (
      head > this.start[0] && (this.end === null || head < this.end[0])
    );
  }

  toString(): string {
    return `[${this.start}-${this.end ?? "+∞"})`;
  }
}
