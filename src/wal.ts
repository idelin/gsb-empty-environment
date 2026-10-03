import { appendFileSync, existsSync, readFileSync, mkdirSync } from "node:fs";
import { dirname } from "node:path";

/**
 * Append-only write-ahead log. Every state transition is persisted
 * (and fsynced by the OS on close of the sync write) BEFORE the
 * corresponding in-memory transition or outbound message.
 */
export class Wal<TRecord extends { op: string }> {
  private readonly path: string;

  constructor(path: string) {
    this.path = path;
    mkdirSync(dirname(path), { recursive: true });
  }

  append(record: TRecord): void {
    appendFileSync(this.path, JSON.stringify(record) + "\n", "utf8");
  }

  replay(): TRecord[] {
    if (!existsSync(this.path)) return [];
    const raw = readFileSync(this.path, "utf8").trim();
    if (raw === "") return [];
    return raw.split("\n").map((line) => JSON.parse(line) as TRecord);
  }
}
