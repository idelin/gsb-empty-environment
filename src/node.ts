import type { MessageBus } from "./bus.ts";
import type { EnvelopedMessage } from "./messages.ts";
import type { OriginStore, VersionedValue } from "./origin.ts";

/**
 * 每个 key 的状态：
 * - EMPTY：无缓存，无屏障约束；
 * - CLEAN：缓存版本 >= 屏障，且没有更新的失效版本；
 * - STALE_ALLOWED：已知有更新版本（收到失效），但屏障尚未覆盖旧值，仍可读旧值；
 * - BARRIER_WAIT：缓存缺失或版本低于屏障，只能读新版本或等待。
 */
export type StateName = "EMPTY" | "CLEAN" | "STALE_ALLOWED" | "BARRIER_WAIT";

export interface ReadResult extends VersionedValue {
  source: "cache" | "origin";
}

export interface KeyInspection {
  state: StateName;
  barrier: number;
  cachedVersion: number;
}

/**
 * 单 key 状态机。barrier 与 invalidatedTo 均单调递增：
 * 迟到或乱序的旧消息不会让状态回退。
 */
class KeyStateMachine {
  private entry: VersionedValue | null = null;
  private barrier = 0;
  private invalidatedTo = 0;

  get state(): StateName {
    if (this.entry && this.entry.version >= this.barrier) {
      return this.invalidatedTo > this.entry.version
        ? "STALE_ALLOWED"
        : "CLEAN";
    }
    return this.barrier > 0 ? "BARRIER_WAIT" : "EMPTY";
  }

  get barrierVersion(): number {
    return this.barrier;
  }

  get cachedVersion(): number {
    return this.entry?.version ?? 0;
  }

  onInvalidate(version: number): void {
    if (version > this.invalidatedTo) this.invalidatedTo = version;
  }

  onBarrier(version: number): void {
    if (version > this.barrier) this.barrier = version;
  }

  /**
   * 加载完成时尝试写入缓存。
   * 若加载结果低于当前屏障（迟到的旧加载），或不比现有缓存更新，则丢弃。
   */
  onLoadComplete(entry: VersionedValue): boolean {
    if (entry.version < this.barrier) return false;
    if (this.entry && entry.version <= this.entry.version) return false;
    this.entry = { ...entry };
    return true;
  }

  /** 仅当缓存版本满足屏障时才可服务。 */
  servable(): VersionedValue | null {
    return this.entry && this.entry.version >= this.barrier ? this.entry : null;
  }
}

export class CacheNode {
  readonly id: string;
  private bus: MessageBus;
  private origin: OriginStore;
  private keys = new Map<string, KeyStateMachine>();

  constructor(id: string, bus: MessageBus, origin: OriginStore) {
    this.id = id;
    this.bus = bus;
    this.origin = origin;
    bus.connect(id, (msg) => this.onMessage(msg));
  }

  inspect(key: string): KeyInspection {
    const sm = this.machine(key);
    return {
      state: sm.state,
      barrier: sm.barrierVersion,
      cachedVersion: sm.cachedVersion,
    };
  }

  /**
   * 读取一个 key。
   * 屏障前：缓存命中（含旧值）直接返回；
   * 屏障后：只能返回 >= 屏障的版本，否则挂起等待数据复制完成。
   * 在途加载若在屏障发布后返回旧版本，会被丢弃并以新屏障重试。
   */
  async read(key: string): Promise<ReadResult> {
    for (;;) {
      const sm = this.machine(key);
      const hit = sm.servable();
      if (hit) return { ...hit, source: "cache" };
      const data = await this.origin.fetch(key, sm.barrierVersion);
      if (this.machine(key).onLoadComplete(data)) {
        return { ...data, source: "origin" };
      }
      // 加载结果被丢弃（迟到旧加载或已有更新缓存），按最新屏障重试。
    }
  }

  disconnect(): void {
    this.bus.disconnect(this.id);
  }

  reconnect(): void {
    this.bus.reconnect(this.id);
  }

  private machine(key: string): KeyStateMachine {
    let sm = this.keys.get(key);
    if (!sm) {
      sm = new KeyStateMachine();
      this.keys.set(key, sm);
    }
    return sm;
  }

  private onMessage(msg: EnvelopedMessage): void {
    const sm = this.machine(msg.key);
    if (msg.kind === "invalidate") sm.onInvalidate(msg.version);
    else sm.onBarrier(msg.version);
  }
}
