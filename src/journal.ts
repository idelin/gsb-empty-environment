import {
  closeSync,
  existsSync,
  fsyncSync,
  openSync,
  readFileSync,
  writeSync,
} from "node:fs";

/**
 * Append-only JSON-lines journal. Every append is followed by fsync, so a
 * committed record survives a process crash. A torn trailing line (partial
 * write at crash time) is tolerated on read; corruption anywhere else is
 * reported.
 */
export class Journal {
  readonly path: string;
  private fd: number;

  constructor(path: string) {
    this.path = path;
    this.fd = openSync(path, "a");
  }

  append(record: unknown): void {
    writeSync(this.fd, JSON.stringify(record) + "\n");
    fsyncSync(this.fd);
  }

  close(): void {
    try {
      closeSync(this.fd);
    } catch {
      // already closed
    }
  }

  static readAll(path: string): unknown[] {
    if (!existsSync(path)) return [];
    const text = readFileSync(path, "utf8");
    const lines = text.split("\n");
    const out: unknown[] = [];
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.length === 0) continue;
      try {
        out.push(JSON.parse(line));
      } catch (err) {
        const onlyBlankTail = lines.slice(i + 1).every((l) => l.length === 0);
        if (onlyBlankTail) break; // torn tail write from a crash: ignore
        throw new Error(`corrupt journal ${path} at line ${i + 1}: ${err}`);
      }
    }
    return out;
  }
}
