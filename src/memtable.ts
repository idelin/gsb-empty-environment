import type { Entry } from "./entry.ts";
import { compareKeys } from "./entry.ts";

export class MemTable {
  private map = new Map<string, Entry>();

  put(key: string, value: string | null, seq: number): void {
    this.map.set(key, { key, value, seq });
  }

  get(key: string): Entry | undefined {
    return this.map.get(key);
  }

  get size(): number {
    return this.map.size;
  }

  sortedEntries(): Entry[] {
    return [...this.map.values()].sort(compareKeys);
  }

  clear(): void {
    this.map.clear();
  }
}
