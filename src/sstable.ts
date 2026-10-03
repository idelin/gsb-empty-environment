import { promises as fs } from "node:fs";
import path from "node:path";
import type { Entry } from "./entry.ts";
import { compareKeys } from "./entry.ts";

interface SSTableFileFormat {
  id: number;
  entries: Entry[];
}

export class SSTable {
  readonly id: number;
  readonly filePath: string;
  private entries: Entry[];
  private index: Map<string, Entry>;

  constructor(id: number, filePath: string, entries: Entry[]) {
    this.id = id;
    this.filePath = filePath;
    this.entries = entries;
    this.index = new Map(entries.map((entry) => [entry.key, entry]));
  }

  static async write(dir: string, id: number, entries: Entry[]): Promise<SSTable> {
    const filePath = path.join(dir, `sst-${String(id).padStart(6, "0")}.json`);
    const sorted = [...entries].sort(compareKeys);
    const payload: SSTableFileFormat = { id, entries: sorted };
    await fs.writeFile(filePath, JSON.stringify(payload, null, 2));
    return new SSTable(id, filePath, sorted);
  }

  static async read(filePath: string): Promise<SSTable> {
    const raw = await fs.readFile(filePath, "utf8");
    const parsed = JSON.parse(raw) as SSTableFileFormat;
    return new SSTable(parsed.id, filePath, parsed.entries);
  }

  get(key: string): Entry | undefined {
    return this.index.get(key);
  }

  all(): Entry[] {
    return this.entries;
  }

  get size(): number {
    return this.entries.length;
  }

  async destroy(): Promise<void> {
    await fs.unlink(this.filePath);
  }
}
