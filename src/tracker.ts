/** 资源泄漏追踪器: 测试末尾断言严格无泄漏。 */

export class ResourceTracker {
  private readonly live = new Map<string, number>();
  private highWater = 0;

  acquire(kind: string): void {
    const n = (this.live.get(kind) ?? 0) + 1;
    this.live.set(kind, n);
    if (n > this.highWater) this.highWater = n;
  }

  release(kind: string): void {
    const n = (this.live.get(kind) ?? 0) - 1;
    if (n < 0) throw new Error(`资源重复释放: ${kind}`);
    if (n === 0) this.live.delete(kind);
    else this.live.set(kind, n);
  }

  get liveCount(): number {
    let total = 0;
    for (const n of this.live.values()) total += n;
    return total;
  }

  get peakConcurrency(): number {
    return this.highWater;
  }

  assertNoLeaks(): void {
    if (this.liveCount !== 0) {
      const detail = [...this.live.entries()]
        .map(([k, n]) => `${k}×${n}`)
        .join(', ');
      throw new Error(`资源泄漏: ${detail}`);
    }
  }
}
