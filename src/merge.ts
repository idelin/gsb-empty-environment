import { compare, type VectorClock } from "./vectorClock.ts";

export interface FieldVersion {
  value: unknown;
  deleted: boolean;
  ts: number;
  replicaId: string;
  clock: VectorClock;
}

export type MergeStrategy = "lww" | "multi-value";

export interface FieldEntry {
  versions: FieldVersion[];
}

function beatsLww(a: FieldVersion, b: FieldVersion): boolean {
  if (a.ts !== b.ts) return a.ts > b.ts;
  return a.replicaId > b.replicaId;
}

export function applyToEntry(
  entry: FieldEntry | undefined,
  version: FieldVersion,
  strategy: MergeStrategy,
): FieldEntry {
  const survivors: FieldVersion[] = [];
  let dominated = false;
  for (const existing of entry?.versions ?? []) {
    const order = compare(version.clock, existing.clock);
    if (order === "before" || order === "equal") {
      dominated = true;
      survivors.push(existing);
    } else if (order === "concurrent") {
      survivors.push(existing);
    }
  }
  if (dominated) return { versions: survivors };
  survivors.push(version);
  if (strategy === "lww") {
    let winner = survivors[0];
    for (const candidate of survivors) {
      if (beatsLww(candidate, winner)) winner = candidate;
    }
    return { versions: [winner] };
  }
  return { versions: survivors };
}
