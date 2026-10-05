import { promises as fs } from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type { TreeEvent } from "./events.ts";

export interface SpillWriter {
  write(ev: TreeEvent): Promise<void>;
  readAll(): Promise<TreeEvent[]>;
  dispose(): Promise<void>;
}

/**
 * File-backed spill store. Buffers that exceed their in-memory limit are
 * written to JSONL files inside a dedicated temp directory; `disposeAll`
 * removes every file and the directory (also used for cancellation cleanup).
 */
export class FileSpillStore {
  private dir: string | null = null;
  private readonly files = new Set<string>();
  private readonly baseDir: string;
  private counter = 0;

  constructor(baseDir?: string) {
    this.baseDir = baseDir ?? os.tmpdir();
  }

  get directory(): string | null {
    return this.dir;
  }

  get spilledFileCount(): number {
    return this.files.size;
  }

  private async ensureDir(): Promise<string> {
    if (this.dir === null) {
      this.dir = await fs.mkdtemp(path.join(this.baseDir, "jsonq-spill-"));
    }
    return this.dir;
  }

  async createWriter(): Promise<SpillWriter> {
    const dir = await this.ensureDir();
    const file = path.join(dir, `rec-${this.counter++}.jsonl`);
    this.files.add(file);
    const handle = await fs.open(file, "w");
    let closed = false;
    let pending = "";
    const flush = async (): Promise<void> => {
      if (pending.length > 0) {
        const chunk = pending;
        pending = "";
        await handle.appendFile(chunk, "utf8");
      }
    };
    return {
      async write(ev: TreeEvent): Promise<void> {
        pending += JSON.stringify(ev) + "\n";
        if (pending.length > 65536) await flush();
      },
      async readAll(): Promise<TreeEvent[]> {
        await flush();
        if (!closed) {
          await handle.close();
          closed = true;
        }
        const text = await fs.readFile(file, "utf8");
        const events: TreeEvent[] = [];
        for (const line of text.split("\n")) {
          if (line.length > 0) events.push(JSON.parse(line) as TreeEvent);
        }
        return events;
      },
      dispose: async (): Promise<void> => {
        pending = "";
        if (!closed) {
          await handle.close();
          closed = true;
        }
        this.files.delete(file);
        await fs.rm(file, { force: true });
      },
    };
  }

  async disposeAll(): Promise<void> {
    const dir = this.dir;
    this.dir = null;
    this.files.clear();
    if (dir !== null) {
      await fs.rm(dir, { recursive: true, force: true });
    }
  }
}
