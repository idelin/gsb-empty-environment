/**
 * 进程内模拟网络：支持连通性矩阵、网络分区与消息统计。
 * 投递为同步调用，保证测试的确定性。
 */
import type { PaxosMsg } from "./types.ts";

export interface NetworkStats {
  sent: number;
  dropped: number;
}

export class InMemoryNetwork {
  private handlers = new Map<string, (msg: PaxosMsg) => void>();
  private links = new Map<string, boolean>();
  readonly stats: NetworkStats = { sent: 0, dropped: 0 };
  /** 测试钩子：返回 true 则丢弃该消息（模拟消息级丢失）。 */
  dropFilter: ((from: string, to: string, msg: PaxosMsg) => boolean) | null = null;

  register(nodeId: string, handler: (msg: PaxosMsg) => void): void {
    this.handlers.set(nodeId, handler);
  }

  unregister(nodeId: string): void {
    this.handlers.delete(nodeId);
  }

  private key(a: string, b: string): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  /** 切断 from->to 双向链路。 */
  sever(a: string, b: string): void {
    this.links.set(this.key(a, b), false);
  }

  heal(a: string, b: string): void {
    this.links.delete(this.key(a, b));
  }

  /** 将节点集合切成两个互不连通的阵营。 */
  partition(groupA: string[], groupB: string[]): void {
    for (const a of groupA) for (const b of groupB) this.sever(a, b);
  }

  healAll(): void {
    this.links.clear();
  }

  connected(a: string, b: string): boolean {
    return this.links.get(this.key(a, b)) !== false;
  }

  send(from: string, to: string, msg: PaxosMsg): boolean {
    this.stats.sent++;
    if (from !== to && !this.connected(from, to)) {
      this.stats.dropped++;
      return false;
    }
    if (this.dropFilter?.(from, to, msg)) {
      this.stats.dropped++;
      return false;
    }
    const handler = this.handlers.get(to);
    if (!handler) {
      this.stats.dropped++;
      return false;
    }
    handler(msg);
    return true;
  }

  broadcast(from: string, targets: Iterable<string>, msg: PaxosMsg): void {
    for (const t of targets) this.send(from, t, msg);
  }
}
