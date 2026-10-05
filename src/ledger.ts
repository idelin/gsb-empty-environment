import { appendFileSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import { dirname } from "node:path";

export interface LedgerEvent {
  ev: string;
  tick: number;
  [key: string]: unknown;
}

export class Ledger {
  readonly path: string;
  private seq = 0;

  constructor(path: string) {
    this.path = path;
    mkdirSync(dirname(path), { recursive: true });
    if (existsSync(path)) {
      this.seq = Ledger.read(path).length;
    }
  }

  append(event: LedgerEvent): void {
    appendFileSync(this.path, JSON.stringify({ seq: this.seq++, ...event }) + "\n");
  }

  static read(path: string): LedgerEvent[] {
    if (!existsSync(path)) return [];
    return readFileSync(path, "utf8")
      .split("\n")
      .filter((line) => line.length > 0)
      .map((line) => JSON.parse(line) as LedgerEvent);
  }
}
