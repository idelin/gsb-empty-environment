import { CausalNode } from "./node.ts";
import type { RecvResult } from "./node.ts";
import type { Message, MessageId, PrefixTable, Reclamation } from "./types.ts";
import { prefixKey } from "./types.ts";

// 旧 epoch 退休记录(墓碑): 需要所有当前成员的确认屏障才 complete。
export interface Retirement {
  node: string;
  epoch: number;
  finalCounter: number;
  complete: boolean;
}

export type Event =
  | { kind: "recv"; at: string; msg: Message }
  | { kind: "ack"; from: string; prefix: PrefixTable }
  | {
      kind: "epochSwitch";
      node: string;
      oldEpoch: number;
      newEpoch: number;
      finalCounter: number;
    }
  | { kind: "checkpoint" };

export class Cluster {
  readonly stagingCapacity: number;
  private readonly nodesMap = new Map<string, CausalNode>();
  private readonly confirmed = new Map<string, PrefixTable>(); // 成员最近确认的前缀
  private readonly retirements: Retirement[] = [];
  private readonly order: { at: string; msg: Message }[] = [];
  private readonly reclamations: Reclamation[] = [];
  private readonly reclaimedUpto = new Map<string, number>(); // 集群级回收水位
  readonly backpressureLog: { at: string; id: MessageId }[] = [];

  constructor(memberIds: string[], stagingCapacity: number) {
    this.stagingCapacity = stagingCapacity;
    for (const id of memberIds) {
      this.nodesMap.set(id, new CausalNode(id, stagingCapacity));
      this.confirmed.set(id, {});
    }
  }

  node(id: string): CausalNode {
    const n = this.nodesMap.get(id);
    if (n === undefined) throw new Error(`unknown member ${id}`);
    return n;
  }

  memberIds(): string[] {
    return [...this.nodesMap.keys()];
  }

  confirmedPrefix(member: string): PrefixTable {
    const prefix = this.confirmed.get(member);
    if (prefix === undefined) throw new Error(`unknown member ${member}`);
    return { ...prefix };
  }

  apply(event: Event): RecvResult | null {
    switch (event.kind) {
      case "recv": {
        const res = this.node(event.at).recv(event.msg);
        for (const msg of res.delivered) this.order.push({ at: event.at, msg });
        if (res.status === "backpressure") {
          this.backpressureLog.push({ at: event.at, id: event.msg.id });
        }
        return res;
      }
      case "ack": {
        this.confirmed.set(event.from, { ...event.prefix });
        this.checkRetirements();
        return null;
      }
      case "epochSwitch": {
        this.node(event.node).switchEpoch(event.newEpoch);
        // 控制面通告: 所有成员给旧 epoch 打封印。
        for (const n of this.nodesMap.values()) {
          n.sealEpoch(event.node, event.oldEpoch, event.finalCounter);
        }
        this.retirements.push({
          node: event.node,
          epoch: event.oldEpoch,
          finalCounter: event.finalCounter,
          complete: false,
        });
        // 重启节点天然知道自己旧 epoch 的完整前缀。
        const self = this.confirmed.get(event.node);
        if (self !== undefined) {
          const key = prefixKey(event.node, event.oldEpoch);
          self[key] = Math.max(self[key] ?? 0, event.finalCounter);
        }
        this.checkRetirements();
        return null;
      }
      case "checkpoint": {
        const stable = this.stablePrefix();
        // 集群级回收区间: 共同稳定点的水位推进(每前缀只报一次)。
        for (const [key, upto] of Object.entries(stable)) {
          const prev = this.reclaimedUpto.get(key) ?? 0;
          if (upto > prev) {
            this.reclamations.push({ kind: "dedup", prefix: key, from: prev + 1, to: upto });
            this.reclaimedUpto.set(key, upto);
          }
        }
        // 各节点按共同稳定前缀回收本地 dedup 记录。
        for (const n of this.nodesMap.values()) n.reclaim(stable);
        // 墓碑只有在屏障完成且共同稳定前缀覆盖整个旧 epoch 后才可回收。
        for (let i = this.retirements.length - 1; i >= 0; i--) {
          const r = this.retirements[i];
          const key = prefixKey(r.node, r.epoch);
          if (r.complete && (stable[key] ?? 0) >= r.finalCounter) {
            this.reclamations.push({
              kind: "tombstone",
              prefix: key,
              from: 1,
              to: r.finalCounter,
            });
            this.retirements.splice(i, 1);
          }
        }
        return null;
      }
    }
  }

  // 共同稳定点 = 所有当前成员确认前缀的逐键最小值,
  // 绝不能用单个节点的最大计数替代。
  stablePrefix(): PrefixTable {
    const keys = new Set<string>();
    for (const prefix of this.confirmed.values()) {
      for (const key of Object.keys(prefix)) keys.add(key);
    }
    const stable: PrefixTable = {};
    for (const key of keys) {
      let min = Infinity;
      for (const prefix of this.confirmed.values()) {
        min = Math.min(min, prefix[key] ?? 0);
      }
      stable[key] = min;
    }
    return stable;
  }

  private checkRetirements(): void {
    for (const r of this.retirements) {
      if (r.complete) continue;
      const key = prefixKey(r.node, r.epoch);
      const all = [...this.confirmed.values()].every(
        (prefix) => (prefix[key] ?? 0) >= r.finalCounter,
      );
      if (all) r.complete = true;
    }
  }

  // ---- 输出 ----

  deliveries(): { at: string; id: MessageId }[] {
    return this.order.map((e) => ({ at: e.at, id: e.msg.id }));
  }

  pending(): { at: string; id: MessageId; missing: string[] }[] {
    const out: { at: string; id: MessageId; missing: string[] }[] = [];
    for (const [at, n] of this.nodesMap) {
      for (const p of n.pending()) out.push({ at, id: p.id, missing: p.missing });
    }
    return out;
  }

  reclaimed(): Reclamation[] {
    return [...this.reclamations];
  }

  retirementStatus(): Retirement[] {
    return this.retirements.map((r) => ({ ...r }));
  }

  resources(): {
    staged: number;
    dedup: number;
    tombstones: number;
    prefixEntries: number;
  } {
    let staged = 0;
    let dedup = 0;
    let prefixEntries = 0;
    for (const n of this.nodesMap.values()) {
      const r = n.resources();
      staged += r.staged;
      dedup += r.dedup;
      prefixEntries += r.prefixEntries;
    }
    return { staged, dedup, tombstones: this.retirements.length, prefixEntries };
  }
}
