/**
 * cdc.ts — 持续变更流模拟器。
 *
 * 模拟真实 CDC 场景：源库不断产生 INSERT / UPDATE / DELETE，
 * 变更事件被复制到目标库。支持注入两类故障：
 *   - 丢数：复制事件被悄悄丢弃（目标库少行 / 多行）；
 *   - 篡改：目标库某行的单个字段被直接改写（绕过复制链路）。
 */
import type { Row, TableStore } from "./store.ts";

/** 可复现的伪随机数发生器（mulberry32）。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function makeRow(id: number, rand: () => number): Row {
  return {
    id: "u" + id,
    name: "user_" + ((id * 7919) % 100000),
    balance: Math.floor(rand() * 1_000_000) / 100,
    level: id % 8,
    updatedAt: 1_700_000_000_000 + Math.floor(rand() * 86_400_000),
  };
}

export interface FlowStats {
  inserts: number;
  updates: number;
  deletes: number;
  replicated: number;
  dropped: number;
}

export class ChangeFlow {
  private nextId: number;
  private readonly liveIds: number[] = [];
  private readonly stats: FlowStats = {
    inserts: 0,
    updates: 0,
    deletes: 0,
    replicated: 0,
    dropped: 0,
  };
  private readonly source: TableStore;
  private readonly target: TableStore;
  private readonly rand: () => number;

  constructor(
    source: TableStore,
    target: TableStore,
    rand: () => number,
    startId = 0,
  ) {
    this.source = source;
    this.target = target;
    this.rand = rand;
    this.nextId = startId;
  }

  get flowStats(): FlowStats {
    return { ...this.stats };
  }

  pickLiveId(): number {
    return this.liveIds[Math.floor(this.rand() * this.liveIds.length)];
  }

  /** 源库产生一批变更，并（可选择性地）复制到目标库。 */
  applyBatch(count: number, dropRate = 0): void {
    for (let i = 0; i < count; i++) {
      const r = this.rand();
      if (r < 0.4 || this.liveIds.length === 0) {
        // INSERT
        const id = this.nextId++;
        const row = makeRow(id, this.rand);
        this.source.put(row);
        this.liveIds.push(id);
        this.stats.inserts++;
        this.replicate("put", row, dropRate);
      } else if (r < 0.85) {
        // UPDATE
        const id = this.pickLiveId();
        const pk = "u" + id;
        const current = this.source.get(pk);
        if (!current) continue;
        const row: Row = {
          ...current,
          balance: Math.floor(this.rand() * 1_000_000) / 100,
          updatedAt: Date.now(),
        };
        this.source.put(row);
        this.stats.updates++;
        this.replicate("put", row, dropRate);
      } else {
        // DELETE
        const idx = Math.floor(this.rand() * this.liveIds.length);
        const id = this.liveIds.splice(idx, 1)[0];
        const pk = "u" + id;
        this.source.delete(pk);
        this.stats.deletes++;
        this.replicate("delete", pk, dropRate);
      }
    }
  }

  private replicate(
    op: "put" | "delete",
    payload: Row | string,
    dropRate: number,
  ): void {
    if (this.rand() < dropRate) {
      this.stats.dropped++; // 丢数：事件在链路上悄悄丢失
      return;
    }
    if (op === "put") this.target.put(payload as Row);
    else this.target.delete(payload as string);
    this.stats.replicated++;
  }

  /** 篡改：绕过复制链路，直接改写目标库某行的单个字段。 */
  tamperOne(pk: string, field: string, value: unknown): boolean {
    const row = this.target.get(pk);
    if (!row) return false;
    this.target.put({ ...row, [field]: value });
    return true;
  }
}
