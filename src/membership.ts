/**
 * Membership table with an incremental hash-ring digest.
 *
 * Every member entry contributes `crc32("id|status|incarnation")` to the
 * table digest via XOR, so inserts / updates / removals are O(1) and the
 * final 4-byte digest is order-independent. The member id space is also
 * mapped onto a fixed set of hash-ring buckets (intervals); each bucket
 * carries its own incremental digest, which lets two nodes locate the
 * diverging key intervals without transferring the full member list.
 */

import { crc32 } from "./crc32.ts";

export type MemberStatus = "alive" | "suspect" | "left" | "dead";

export interface MemberRecord {
  id: string;
  status: MemberStatus;
  incarnation: number;
}

/** SWIM-like precedence used to break ties at equal incarnation. */
const STATUS_RANK: Record<MemberStatus, number> = {
  alive: 0,
  suspect: 1,
  left: 2,
  dead: 3,
};

export function entryChecksum(record: MemberRecord): number {
  return crc32(`${record.id}|${record.status}|${record.incarnation}`);
}

/** True when `incoming` should replace `existing` (last-writer-wins). */
export function shouldReplace(
  existing: MemberRecord,
  incoming: MemberRecord,
): boolean {
  if (incoming.incarnation !== existing.incarnation) {
    return incoming.incarnation > existing.incarnation;
  }
  return STATUS_RANK[incoming.status] > STATUS_RANK[existing.status];
}

export class MembershipTable {
  readonly bucketCount: number;
  private readonly records = new Map<string, MemberRecord>();
  private checksum = 0;
  private readonly bucketChecksums: Uint32Array;

  constructor(bucketCount = 16) {
    this.bucketCount = bucketCount;
    this.bucketChecksums = new Uint32Array(bucketCount);
  }

  bucketOf(memberId: string): number {
    return crc32(memberId) % this.bucketCount;
  }

  /** 4-byte incremental digest of the whole membership view. */
  get digest(): number {
    return this.checksum >>> 0;
  }

  /** Per-interval digests of the hash ring (copy). */
  bucketDigests(): Uint32Array {
    return this.bucketChecksums.slice();
  }

  get(memberId: string): MemberRecord | undefined {
    const found = this.records.get(memberId);
    return found ? { ...found } : undefined;
  }

  size(): number {
    return this.records.size;
  }

  /**
   * Merge one record. Returns true when the local view changed.
   * The digest is updated incrementally: XOR out the old entry
   * checksum, XOR in the new one.
   */
  upsert(record: MemberRecord): boolean {
    const existing = this.records.get(record.id);
    if (existing && !shouldReplace(existing, record)) {
      return false;
    }
    const bucket = this.bucketOf(record.id);
    if (existing) {
      const oldChecksum = entryChecksum(existing);
      this.checksum ^= oldChecksum;
      this.bucketChecksums[bucket] ^= oldChecksum;
    }
    const next: MemberRecord = { ...record };
    const newChecksum = entryChecksum(next);
    this.checksum ^= newChecksum;
    this.bucketChecksums[bucket] ^= newChecksum;
    this.records.set(record.id, next);
    return true;
  }

  /** Merge a batch of records; returns the ones that were accepted. */
  applyAll(records: MemberRecord[]): MemberRecord[] {
    const accepted: MemberRecord[] = [];
    for (const record of records) {
      if (this.upsert(record)) {
        accepted.push({ ...record });
      }
    }
    return accepted;
  }

  recordsInBuckets(buckets: ReadonlySet<number>): MemberRecord[] {
    const result: MemberRecord[] = [];
    for (const record of this.records.values()) {
      if (buckets.has(this.bucketOf(record.id))) {
        result.push({ ...record });
      }
    }
    return result;
  }

  /** Deterministic snapshot sorted by member id. */
  snapshot(): MemberRecord[] {
    return [...this.records.values()]
      .map((record) => ({ ...record }))
      .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  }

  /** Ground-truth digest recomputed from scratch (used by tests/audit). */
  static recomputeDigest(records: Iterable<MemberRecord>): number {
    let digest = 0;
    for (const record of records) {
      digest ^= entryChecksum(record);
    }
    return digest >>> 0;
  }
}
