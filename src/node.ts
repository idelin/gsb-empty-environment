import type { Message, MessageId, PrefixTable, Reclamation } from "./types.ts";
import { idKey, parseIdKey, prefixKey } from "./types.ts";

// 每个 (node, epoch) 前缀的持久状态: 连续交付水位 upto + 退休封印 seal。
// 该状态不可回收(每 epoch 仅两个数字), 可回收的是逐消息的 dedup 记录。
export interface PrefixEntry {
  upto: number;
  seal: number | null; // 非空表示该 epoch 已退休, finalCounter 为最终序号
}

export type RecvStatus =
  | "delivered" // 本次调用交付(可能连带排空暂存)
  | "duplicate" // 幂等重复
  | "staged" // 缺依赖, 进入有界暂存
  | "backpressure" // 暂存已满, 明确背压(消息未被接受)
  | "conflict" // 同身份不同内容
  | "sealed"; // 已退休 epoch 上超出 finalCounter 的非法消息

export interface RecvResult {
  status: RecvStatus;
  missing: string[]; // 待定依赖(人类可读): 前驱 id / "dep<=n" / "sealed:..."
  delivered: Message[]; // 本次调用实际交付的消息(含排空)
}

const result = (status: RecvStatus, missing: string[] = []): RecvResult => ({
  status,
  missing,
  delivered: [],
});

export class CausalNode {
  readonly id: string;
  readonly stagingCapacity: number;
  currentEpoch = 1;

  private readonly prefixes = new Map<string, PrefixEntry>();
  private readonly dedup = new Map<string, string>(); // idKey -> payload
  private readonly staged = new Map<string, Message>(); // idKey -> msg
  private readonly reclaimedUpto = new Map<string, number>();
  private readonly log: Message[] = [];
  private nextCounter = 1;

  constructor(id: string, stagingCapacity: number) {
    this.id = id;
    this.stagingCapacity = stagingCapacity;
  }

  upto(node: string, epoch: number): number {
    return this.prefixes.get(prefixKey(node, epoch))?.upto ?? 0;
  }

  snapshotPrefix(): PrefixTable {
    const out: PrefixTable = {};
    for (const [key, entry] of this.prefixes) out[key] = entry.upto;
    return out;
  }

  delivered(): Message[] {
    return [...this.log];
  }

  pending(): { id: MessageId; missing: string[] }[] {
    return [...this.staged.values()].map((msg) => ({
      id: msg.id,
      missing: this.missingFor(msg),
    }));
  }

  resources(): { staged: number; dedup: number; prefixEntries: number } {
    return {
      staged: this.staged.size,
      dedup: this.dedup.size,
      prefixEntries: this.prefixes.size,
    };
  }

  // 本节点发布: 序号 = 当前 epoch 的下一个连续 counter, 依赖 = 当前观察前缀。
  publish(payload: string): Message {
    const msg: Message = {
      id: { node: this.id, epoch: this.currentEpoch, counter: this.nextCounter },
      deps: this.snapshotPrefix(),
      payload,
    };
    const res = this.recv(msg);
    if (res.status !== "delivered") {
      throw new Error(`self-publish rejected: ${res.status}`);
    }
    this.nextCounter += 1;
    return msg;
  }

  // 成员重启: 切换到更大的 epoch, 本节点计数器重新从 1 开始。
  switchEpoch(newEpoch: number): void {
    if (newEpoch <= this.currentEpoch) throw new Error("epoch must increase");
    this.currentEpoch = newEpoch;
    this.nextCounter = 1;
  }

  // 控制面退休通告: 给旧 epoch 打上封印(最终序号), 之后超出者非法。
  sealEpoch(node: string, epoch: number, finalCounter: number): void {
    const key = prefixKey(node, epoch);
    const entry = this.prefixes.get(key) ?? { upto: 0, seal: null };
    entry.seal = finalCounter;
    this.prefixes.set(key, entry);
  }

  recv(msg: Message): RecvResult {
    const key = idKey(msg.id);
    const seen = this.dedup.get(key);
    if (seen !== undefined) {
      return seen === msg.payload ? result("duplicate") : result("conflict");
    }
    const stagedPrior = this.staged.get(key);
    if (stagedPrior !== undefined) {
      return stagedPrior.payload === msg.payload ? result("duplicate") : result("conflict");
    }

    const pkey = prefixKey(msg.id.node, msg.id.epoch);
    const entry = this.prefixes.get(pkey);
    // dedup 可能已被回收, 但连续前缀仍在: 低于水位者必为重复。
    if (entry !== undefined && msg.id.counter <= entry.upto) return result("duplicate");
    if (entry !== undefined && entry.seal !== null && msg.id.counter > entry.seal) {
      return result("sealed");
    }

    const missing = this.missingFor(msg);
    if (missing.length > 0) {
      if (this.staged.size >= this.stagingCapacity) {
        return result("backpressure", missing); // 明确背压, 不静默丢弃
      }
      this.staged.set(key, msg);
      return result("staged", missing);
    }

    const delivered: Message[] = [];
    this.deliver(msg, delivered);
    this.drain(delivered);
    return { status: "delivered", missing: [], delivered };
  }

  // 共同稳定前缀达成后回收逐消息 dedup 记录; 返回新回收的区间。
  reclaim(stable: PrefixTable): Reclamation[] {
    const out: Reclamation[] = [];
    for (const [key, upto] of Object.entries(stable)) {
      const prev = this.reclaimedUpto.get(key) ?? 0;
      if (upto > prev) {
        out.push({ kind: "dedup", prefix: key, from: prev + 1, to: upto });
        this.reclaimedUpto.set(key, upto);
      }
    }
    for (const key of [...this.dedup.keys()]) {
      const id = parseIdKey(key);
      if ((stable[prefixKey(id.node, id.epoch)] ?? 0) >= id.counter) {
        this.dedup.delete(key);
      }
    }
    return out;
  }

  private missingFor(msg: Message): string[] {
    const pkey = prefixKey(msg.id.node, msg.id.epoch);
    const entry = this.prefixes.get(pkey);
    if (entry !== undefined && entry.seal !== null && msg.id.counter > entry.seal) {
      return [`sealed:${pkey}`];
    }
    const upto = entry?.upto ?? 0;
    const missing: string[] = [];
    // 本节点序号必须连续: 高序号不能自动填缺口, 缺的是直接前驱。
    if (msg.id.counter > upto + 1) {
      missing.push(
        idKey({ node: msg.id.node, epoch: msg.id.epoch, counter: upto + 1 }),
      );
    }
    for (const [dep, need] of Object.entries(msg.deps)) {
      if ((this.prefixes.get(dep)?.upto ?? 0) < need) missing.push(`${dep}<=${need}`);
    }
    return missing;
  }

  private deliver(msg: Message, sink: Message[]): void {
    const pkey = prefixKey(msg.id.node, msg.id.epoch);
    const entry = this.prefixes.get(pkey) ?? { upto: 0, seal: null };
    if (msg.id.counter !== entry.upto + 1) {
      throw new Error(`continuity violation at ${idKey(msg.id)}`);
    }
    entry.upto = msg.id.counter;
    this.prefixes.set(pkey, entry);
    this.dedup.set(idKey(msg.id), msg.payload);
    this.log.push(msg);
    sink.push(msg);
  }

  private drain(sink: Message[]): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const key of [...this.staged.keys()].sort()) {
        const msg = this.staged.get(key);
        if (msg === undefined) continue;
        const upto = this.upto(msg.id.node, msg.id.epoch);
        if (msg.id.counter <= upto) {
          this.staged.delete(key); // 已被前缀覆盖
          continue;
        }
        if (this.missingFor(msg).length === 0) {
          this.staged.delete(key);
          this.deliver(msg, sink);
          progressed = true;
        }
      }
    }
  }
}
