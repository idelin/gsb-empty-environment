// 按 (time, id) 排序的事件索引。时间相同按事件 ID 排序（题目要求的确定序）。
// 用有序数组 + 二分实现；开窗内事件数决定了它的大小。

export class TimeIndex {
  private times: number[] = [];
  private ids: string[] = [];

  get size(): number {
    return this.times.length;
  }

  private lowerBound(time: number, id: string): number {
    let lo = 0;
    let hi = this.times.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      const t = this.times[mid];
      if (t < time || (t === time && this.ids[mid] < id)) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  insert(time: number, id: string): void {
    const i = this.lowerBound(time, id);
    this.times.splice(i, 0, time);
    this.ids.splice(i, 0, id);
  }

  remove(time: number, id: string): void {
    const i = this.lowerBound(time, id);
    if (i < this.times.length && this.times[i] === time && this.ids[i] === id) {
      this.times.splice(i, 1);
      this.ids.splice(i, 1);
    }
  }

  /** [lo, hi] 闭区间内的 id 列表（按 (time,id) 序）。 */
  rangeClosed(lo: number, hi: number): string[] {
    const start = this.lowerBound(lo, '');
    const out: string[] = [];
    for (let i = start; i < this.times.length && this.times[i] <= hi; i++) {
      out.push(this.ids[i]);
    }
    return out;
  }

  /** [lo, hi) 区间内的 id 列表。 */
  rangeClosedOpen(lo: number, hi: number): string[] {
    const start = this.lowerBound(lo, '');
    const out: string[] = [];
    for (let i = start; i < this.times.length && this.times[i] < hi; i++) {
      out.push(this.ids[i]);
    }
    return out;
  }

  /** 最小 >= time 的时间，不存在返回 undefined。 */
  firstTimeAtOrAfter(time: number): number | undefined {
    const i = this.lowerBound(time, '');
    return i < this.times.length ? this.times[i] : undefined;
  }

  /** 弹出所有 time < horizon 的条目（用于水位线裁剪）。 */
  drainBefore(horizon: number): Array<{ time: number; id: string }> {
    let n = 0;
    while (n < this.times.length && this.times[n] < horizon) n++;
    const out: Array<{ time: number; id: string }> = [];
    for (let i = 0; i < n; i++) out.push({ time: this.times[i], id: this.ids[i] });
    this.times.splice(0, n);
    this.ids.splice(0, n);
    return out;
  }

  toJSON(): { times: number[]; ids: string[] } {
    return { times: [...this.times], ids: [...this.ids] };
  }

  static fromJSON(data: { times: number[]; ids: string[] }): TimeIndex {
    const idx = new TimeIndex();
    idx.times = [...data.times];
    idx.ids = [...data.ids];
    return idx;
  }
}
