import { compareDots, dotKey, VersionVector } from "./versionVector.ts";
import type { Dot } from "./versionVector.ts";

export interface Sibling<T> {
  readonly dot: Dot;
  readonly value: T;
}

export interface DVVEntry<T> {
  readonly context: VersionVector;
  readonly siblings: readonly Sibling<T>[];
}

export interface ReadResult<T> {
  readonly values: readonly T[];
  readonly context: VersionVector;
}

export interface WriteResult {
  readonly dot: Dot;
  readonly context: VersionVector;
}

export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value) ?? "undefined";
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const body = keys
    .map((key) => `${JSON.stringify(key)}:${canonicalize(record[key])}`)
    .join(",");
  return `{${body}}`;
}

function emptyEntry<T>(): DVVEntry<T> {
  return { context: VersionVector.empty(), siblings: [] };
}

export function entryContext<T>(entry: DVVEntry<T>): VersionVector {
  let context = entry.context;
  for (const sibling of entry.siblings) {
    context = context.fold(sibling.dot);
  }
  return context;
}

export function entrySize<T>(entry: DVVEntry<T>): number {
  return entry.context.size + entry.siblings.length;
}

export class DVVStore<T> {
  readonly nodeId: string;
  private readonly signature: (value: T) => string;
  private readonly entries = new Map<string, DVVEntry<T>>();

  constructor(nodeId: string, signature: (value: T) => string = canonicalize) {
    this.nodeId = nodeId;
    this.signature = signature;
  }

  keys(): string[] {
    return [...this.entries.keys()].sort();
  }

  entry(key: string): DVVEntry<T> {
    return this.entries.get(key) ?? emptyEntry();
  }

  get(key: string): ReadResult<T> {
    const entry = this.entry(key);
    return {
      values: entry.siblings.map((sibling) => sibling.value),
      context: entryContext(entry),
    };
  }

  put(key: string, value: T, readContext: VersionVector = VersionVector.empty()): WriteResult {
    const current = this.entry(key);
    let counter = Math.max(
      current.context.get(this.nodeId),
      readContext.get(this.nodeId),
    );
    for (const sibling of current.siblings) {
      if (sibling.dot.node === this.nodeId && sibling.dot.counter > counter) {
        counter = sibling.dot.counter;
      }
    }
    counter += 1;
    const dot: Dot = { node: this.nodeId, counter };
    let context = current.context.join(readContext).set(this.nodeId, counter);
    const kept: Sibling<T>[] = [];
    const valueSignature = this.signature(value);
    for (const sibling of current.siblings) {
      if (readContext.covers(sibling.dot)) continue;
      if (this.signature(sibling.value) === valueSignature) {
        context = context.fold(sibling.dot);
        continue;
      }
      kept.push(sibling);
    }
    const siblings = [{ dot, value }, ...kept].sort((a, b) => compareDots(a.dot, b.dot));
    const next: DVVEntry<T> = { context, siblings };
    this.entries.set(key, next);
    return { dot, context: entryContext(next) };
  }

  merge(key: string, remote: DVVEntry<T>): void {
    const local = this.entry(key);
    let context = local.context.join(remote.context);
    const remoteDots = new Map<string, Sibling<T>>();
    for (const sibling of remote.siblings) {
      remoteDots.set(dotKey(sibling.dot), sibling);
    }
    const byDot = new Map<string, Sibling<T>>();
    for (const sibling of local.siblings) {
      const id = dotKey(sibling.dot);
      if (!remoteDots.has(id) && remote.context.covers(sibling.dot)) continue;
      byDot.set(id, sibling);
    }
    for (const sibling of remote.siblings) {
      const id = dotKey(sibling.dot);
      const existing = byDot.get(id);
      if (existing !== undefined) {
        const existingSignature = this.signature(existing.value);
        const incomingSignature = this.signature(sibling.value);
        if (incomingSignature < existingSignature) {
          byDot.set(id, sibling);
        }
        continue;
      }
      if (local.context.covers(sibling.dot)) continue;
      byDot.set(id, sibling);
    }
    const ordered = [...byDot.values()].sort((a, b) => compareDots(a.dot, b.dot));
    const kept: Sibling<T>[] = [];
    const seenValues = new Set<string>();
    for (const sibling of ordered) {
      const valueSignature = this.signature(sibling.value);
      if (seenValues.has(valueSignature)) {
        context = context.fold(sibling.dot);
      } else {
        seenValues.add(valueSignature);
        kept.push(sibling);
      }
    }
    this.entries.set(key, { context, siblings: kept });
  }

  snapshot(key: string): { context: Record<string, number>; siblings: { dot: Dot; value: T }[] } {
    const entry = this.entry(key);
    return {
      context: entry.context.toJSON(),
      siblings: entry.siblings.map((sibling) => ({ dot: sibling.dot, value: sibling.value })),
    };
  }
}
