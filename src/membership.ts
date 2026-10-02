import { DigestRing, contributionOf, intervalOf } from "./digest.ts";

export type MemberState = "ALIVE" | "SUSPECT" | "DEAD";

export interface MemberRecord {
  id: string;
  state: MemberState;
  incarnation: number; // 逻辑版本号，冲突合并时取大者（Last-Writer-Wins）
}

// 同 incarnation 下的状态优先级：DEAD > SUSPECT > ALIVE。
const STATE_RANK: Record<MemberState, number> = { ALIVE: 0, SUSPECT: 1, DEAD: 2 };

export const INTERVAL_COUNT = 8;

// 成员表：维护成员 ID/状态，并同步维护全局增量 Digest 与分区间 Digest。
export class MemberTable {
  private records = new Map<string, MemberRecord>();
  private ring = new DigestRing();
  private intervalRings: DigestRing[] = [];

  constructor() {
    for (let i = 0; i < INTERVAL_COUNT; i++) this.intervalRings.push(new DigestRing());
  }

  // 返回 true 表示记录被接受（更新了本地视图）。
  upsert(record: MemberRecord): boolean {
    const existing = this.records.get(record.id);
    if (existing) {
      const newer =
        record.incarnation > existing.incarnation ||
        (record.incarnation === existing.incarnation &&
          STATE_RANK[record.state] > STATE_RANK[existing.state]);
      if (!newer) return false;
      const oldC = contributionOf(existing.id, existing.state, existing.incarnation);
      const newC = contributionOf(record.id, record.state, record.incarnation);
      this.ring.replace(oldC, newC);
      this.intervalRings[intervalOf(record.id, INTERVAL_COUNT)].replace(oldC, newC);
      this.records.set(record.id, { ...record });
      return true;
    }
    const c = contributionOf(record.id, record.state, record.incarnation);
    this.ring.add(c);
    this.intervalRings[intervalOf(record.id, INTERVAL_COUNT)].add(c);
    this.records.set(record.id, { ...record });
    return true;
  }

  get(id: string): MemberRecord | undefined {
    const r = this.records.get(id);
    return r ? { ...r } : undefined;
  }

  all(): MemberRecord[] {
    return [...this.records.values()].map((r) => ({ ...r }));
  }

  // 全局 4 字节摘要（探活时交换比对）。
  get digest(): number {
    return this.ring.digest;
  }

  get digestHex(): string {
    return this.ring.digestHex;
  }

  get size(): number {
    return this.ring.count;
  }

  // 分区间摘要：失配时用于定位分歧区间，只拉取差异区间的记录。
  intervalDigests(): number[] {
    return this.intervalRings.map((r) => r.digest);
  }

  recordsInInterval(interval: number): MemberRecord[] {
    return this.all().filter((r) => intervalOf(r.id, INTERVAL_COUNT) === interval);
  }
}
