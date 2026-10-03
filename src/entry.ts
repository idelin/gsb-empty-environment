export const TOMBSTONE: null = null;

export interface Entry {
  key: string;
  value: string | null;
  seq: number;
}

export function isTombstone(entry: Entry): boolean {
  return entry.value === TOMBSTONE;
}

export function compareKeys(a: Entry, b: Entry): number {
  return a.key < b.key ? -1 : a.key > b.key ? 1 : 0;
}

export function mergeEntries(inputs: Entry[][], dropTombstones: boolean): Entry[] {
  const newest = new Map<string, Entry>();
  for (const list of inputs) {
    for (const entry of list) {
      const current = newest.get(entry.key);
      if (!current || entry.seq > current.seq) {
        newest.set(entry.key, entry);
      }
    }
  }
  const merged = [...newest.values()].sort(compareKeys);
  return dropTombstonesMerged(dropTombstones, merged);
}

function dropTombstonesMerged(drop: boolean, merged: Entry[]): Entry[] {
  return drop ? merged.filter((entry) => !isTombstone(entry)) : merged;
}
