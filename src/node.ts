import { contentHash, idKey, streamKey } from "./types.ts";
import type { Message, MessageId, PrefixVector } from "./types.ts";

export class ConflictError extends Error {
  readonly key: string;
  constructor(key: string) {
    super(`conflicting content for message identity ${key}`);
    this.name = "ConflictError";
    this.key = key;
  }
}

export class BackpressureError extends Error {
  readonly key: string;
  readonly limit: number;
  constructor(key: string, limit: number) {
    super(`staging buffer full (limit ${limit}); refusing message ${key}`);
    this.name = "BackpressureError";
    this.key = key;
    this.limit = limit;
  }
}

export interface MissingDep {
  stream: string;
  need: number;
  have: number;
}

export interface PendingEntry {
  id: MessageId;
  missing: MissingDep[];
}

export interface ReclaimInterval {
  stream: string;
  from: number;
  to: number;
  tombstoneDropped: boolean;
}

export interface ResourceSnapshot {
  staged: number;
  dedupRecords: number;
  tombstoneStreams: number;
  received: number;
  delivered: number;
  duplicates: number;
  conflicts: number;
  backpressure: number;
  reclaimed: number;
  stagedHighWater: number;
}

interface Entry {
  message: Message;
  hash: string;
}

export class CausalBroadcastNode {
  readonly self: string;
  readonly stagingLimit: number;
  currentEpoch = 1;
  readonly deliveryLog: MessageId[] = [];
  readonly stats = {
    received: 0,
    delivered: 0,
    duplicates: 0,
    conflicts: 0,
    backpressure: 0,
    reclaimed: 0,
    stagedHighWater: 0,
  };

  private counter = 0;
  private readonly prefix = new Map<string, number>();
  private readonly delivered = new Map<string, Entry>();
  private readonly staged = new Map<string, Entry>();
  private readonly tombstones = new Map<string, number>();
  private readonly fullyReclaimed = new Set<string>();

  constructor(self: string, stagingLimit = 16) {
    this.self = self;
    this.stagingLimit = stagingLimit;
  }

  prefixOf(stream: string): number {
    return this.prefix.get(stream) ?? 0;
  }

  localPrefixVector(): PrefixVector {
    const vector: PrefixVector = {};
    for (const [stream, counter] of this.prefix) {
      if (counter > 0) vector[stream] = counter;
    }
    return vector;
  }

  switchEpoch(): { stream: string; finalCounter: number } {
    const sealed = {
      stream: streamKey(this.self, this.currentEpoch),
      finalCounter: this.counter,
    };
    this.currentEpoch += 1;
    this.counter = 0;
    return sealed;
  }

  createMessage(payload: unknown): Message {
    this.counter += 1;
    return {
      id: { node: this.self, epoch: this.currentEpoch, counter: this.counter },
      deps: this.localPrefixVector(),
      payload,
    };
  }

  receive(message: Message): Message[] {
    this.stats.received += 1;
    const key = idKey(message.id);
    const hash = contentHash(message);
    const stream = streamKey(message.id.node, message.id.epoch);

    const prior = this.delivered.get(key);
    if (prior) {
      if (prior.hash !== hash) return this.conflict(key);
      this.stats.duplicates += 1;
      return [];
    }
    if (this.prefixOf(stream) >= message.id.counter) {
      this.stats.duplicates += 1;
      return [];
    }
    const pending = this.staged.get(key);
    if (pending) {
      if (pending.hash !== hash) return this.conflict(key);
      this.stats.duplicates += 1;
      return [];
    }

    const deliveredNow: Message[] = [];
    if (this.isDeliverable(message)) {
      this.commit(message, hash, deliveredNow);
      this.drainStaged(deliveredNow);
      return deliveredNow;
    }
    if (this.staged.size >= this.stagingLimit) {
      this.stats.backpressure += 1;
      throw new BackpressureError(key, this.stagingLimit);
    }
    this.staged.set(key, { message, hash });
    this.stats.stagedHighWater = Math.max(this.stats.stagedHighWater, this.staged.size);
    return deliveredNow;
  }

  private conflict(key: string): never {
    this.stats.conflicts += 1;
    throw new ConflictError(key);
  }

  private isDeliverable(message: Message): boolean {
    const stream = streamKey(message.id.node, message.id.epoch);
    if (message.id.counter !== this.prefixOf(stream) + 1) return false;
    for (const [depStream, need] of Object.entries(message.deps)) {
      if (this.prefixOf(depStream) < need) return false;
    }
    return true;
  }

  private commit(message: Message, hash: string, out: Message[]): void {
    const stream = streamKey(message.id.node, message.id.epoch);
    this.prefix.set(stream, message.id.counter);
    this.delivered.set(idKey(message.id), { message, hash });
    this.deliveryLog.push(message.id);
    this.stats.delivered += 1;
    out.push(message);
  }

  private drainStaged(out: Message[]): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const [key, entry] of this.staged) {
        if (this.isDeliverable(entry.message)) {
          this.staged.delete(key);
          this.commit(entry.message, entry.hash, out);
          progressed = true;
        }
      }
    }
  }

  missingDeps(message: Message): MissingDep[] {
    const missing: MissingDep[] = [];
    const stream = streamKey(message.id.node, message.id.epoch);
    const have = this.prefixOf(stream);
    if (message.id.counter > have + 1) {
      missing.push({ stream, need: message.id.counter - 1, have });
    }
    for (const [depStream, need] of Object.entries(message.deps)) {
      const haveDep = this.prefixOf(depStream);
      if (haveDep < need) missing.push({ stream: depStream, need, have: haveDep });
    }
    return missing;
  }

  pendingDependencies(): PendingEntry[] {
    return [...this.staged.values()].map((entry) => ({
      id: entry.message.id,
      missing: this.missingDeps(entry.message),
    }));
  }

  reclaim(stable: PrefixVector, retiredFinal: ReadonlyMap<string, number>): ReclaimInterval[] {
    const intervals: ReclaimInterval[] = [];
    for (const [stream, stableCounter] of Object.entries(stable)) {
      if (this.fullyReclaimed.has(stream)) continue;
      const current = this.tombstones.get(stream) ?? 0;
      const final = retiredFinal.get(stream);
      const dropTombstone = final !== undefined && stableCounter >= final;
      if (stableCounter <= current && !dropTombstone) continue;
      let reclaimed = 0;
      if (stableCounter > current) {
        for (const [key, entry] of this.delivered) {
          const id = entry.message.id;
          if (streamKey(id.node, id.epoch) === stream && id.counter <= stableCounter) {
            this.delivered.delete(key);
            reclaimed += 1;
          }
        }
        this.tombstones.set(stream, stableCounter);
      }
      if (dropTombstone) {
        this.tombstones.delete(stream);
        this.fullyReclaimed.add(stream);
      }
      this.stats.reclaimed += reclaimed;
      intervals.push({ stream, from: current + 1, to: stableCounter, tombstoneDropped: dropTombstone });
    }
    return intervals;
  }

  resources(): ResourceSnapshot {
    return {
      staged: this.staged.size,
      dedupRecords: this.delivered.size,
      tombstoneStreams: this.tombstones.size,
      ...this.stats,
    };
  }
}
