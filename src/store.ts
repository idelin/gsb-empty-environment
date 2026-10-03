/**
 * store.ts — 内存表存储，模拟源库 / 目标库。
 *
 * 每次写入/删除同步维护 ChecksumIndex（滚动 XOR），
 * 使校验和始终与当前数据一致，无需周期性全表重算。
 */
import { ChecksumIndex } from "./checksum.ts";
import { hashString, type Hash64 } from "./hash.ts";

export interface Row {
  id: string;
  [field: string]: unknown;
}

/** 稳定序列化：键排序后 JSON 化，保证两端对同一行得到同一字节流。 */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) {
    return "[" + value.map(canonicalize).join(",") + "]";
  }
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return (
    "{" +
    keys.map((k) => JSON.stringify(k) + ":" + canonicalize(obj[k])).join(",") +
    "}"
  );
}

export function hashRow(row: Row): Hash64 {
  return hashString(canonicalize(row));
}

export class TableStore {
  readonly name: string;
  readonly index: ChecksumIndex;
  private readonly rows = new Map<string, Row>();
  private readonly partitionMembers: Array<Set<string>>;

  constructor(name: string, partitionCount = 1024) {
    this.name = name;
    this.index = new ChecksumIndex(partitionCount);
    this.partitionMembers = Array.from(
      { length: partitionCount },
      () => new Set<string>(),
    );
  }

  get size(): number {
    return this.rows.size;
  }

  get(pk: string): Row | undefined {
    return this.rows.get(pk);
  }

  /** 插入或覆盖一行，并增量更新校验和。 */
  put(row: Row): void {
    const pk = row.id;
    const p = this.index.partitionOf(pk);
    const existing = this.rows.get(pk);
    const oldHash = existing ? hashRow(existing) : null;
    const newHash = hashRow(row);
    this.rows.set(pk, row);
    if (!existing) this.partitionMembers[p].add(pk);
    this.index.apply(pk, oldHash, newHash);
  }

  delete(pk: string): boolean {
    const existing = this.rows.get(pk);
    if (!existing) return false;
    const p = this.index.partitionOf(pk);
    this.rows.delete(pk);
    this.partitionMembers[p].delete(pk);
    this.index.apply(pk, hashRow(existing), null);
    return true;
  }

  /** 遍历指定分区内的所有行（用于失配下钻）。 */
  *rowsInPartition(p: number): IterableIterator<Row> {
    for (const pk of this.partitionMembers[p]) {
      yield this.rows.get(pk)!;
    }
  }
}
