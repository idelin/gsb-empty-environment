import type { MessageBus } from "./bus.ts";

export interface VersionedValue {
  value: string;
  version: number;
}

/**
 * 配置源站。写入先落到 committed（并广播失效消息），
 * 需要显式 replicate 后才对节点可见 —— 因此失效消息可以先于数据到达。
 * fetch(key, minVersion) 只有在可见版本 >= minVersion 时才返回，否则挂起等待。
 */
export class OriginStore {
  private bus: MessageBus;
  private committed = new Map<string, VersionedValue>();
  private visible = new Map<string, VersionedValue>();
  private versions = new Map<string, number>();
  private waiters: Array<() => void> = [];
  private gate: Promise<void> | null = null;
  private releaseGate: (() => void) | null = null;

  constructor(bus: MessageBus) {
    this.bus = bus;
  }

  committedVersion(key: string): number {
    return this.versions.get(key) ?? 0;
  }

  /** 写入新版本并广播失效消息（数据此时尚不可被节点加载）。 */
  update(key: string, value: string): number {
    const version = this.committedVersion(key) + 1;
    this.versions.set(key, version);
    this.committed.set(key, { value, version });
    this.bus.publish({ kind: "invalidate", key, version });
    return version;
  }

  /** 发布屏障：此后节点只能返回 >= version 的数据，否则等待。 */
  publishBarrier(key: string, version?: number): void {
    this.bus.publish({
      kind: "barrier",
      key,
      version: version ?? this.committedVersion(key),
    });
  }

  /** 让已提交的数据对节点可见（模拟复制延迟结束）。 */
  replicate(key: string): void {
    const entry = this.committed.get(key);
    if (entry) this.visible.set(key, entry);
    this.notifyWaiters();
  }

  visibleEntry(key: string): VersionedValue | undefined {
    return this.visible.get(key);
  }

  /** 测试钩子：让后续 fetch 挂起，模拟加载在途。 */
  holdFetches(): void {
    if (!this.gate) {
      this.gate = new Promise<void>((resolve) => {
        this.releaseGate = resolve;
      });
    }
  }

  releaseFetches(): void {
    this.releaseGate?.();
    this.gate = null;
    this.releaseGate = null;
  }

  async fetch(key: string, minVersion: number): Promise<VersionedValue> {
    if (this.gate) await this.gate;
    for (;;) {
      const entry = this.visible.get(key);
      if (entry && entry.version >= minVersion) return entry;
      await new Promise<void>((resolve) => this.waiters.push(resolve));
    }
  }

  private notifyWaiters(): void {
    const pending = this.waiters;
    this.waiters = [];
    for (const resolve of pending) resolve();
  }
}
