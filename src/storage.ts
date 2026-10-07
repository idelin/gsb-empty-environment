import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';

/** 同步存储接口：引擎在每次状态变更后持久化快照，重启时从快照恢复。 */
export interface Storage {
  load(): string | null;
  save(snapshot: string): void;
}

export class MemoryStorage implements Storage {
  private snapshot: string | null = null;
  load(): string | null {
    return this.snapshot;
  }
  save(snapshot: string): void {
    this.snapshot = snapshot;
  }
}

export class FileStorage implements Storage {
  private readonly path: string;

  constructor(path: string) {
    this.path = path;
  }
  load(): string | null {
    try {
      return readFileSync(this.path, 'utf8');
    } catch (err) {
      if ((err as { code?: string }).code === 'ENOENT') return null;
      throw err;
    }
  }
  save(snapshot: string): void {
    mkdirSync(dirname(this.path), { recursive: true });
    const tmp = `${this.path}.tmp`;
    writeFileSync(tmp, snapshot, 'utf8');
    renameSync(tmp, this.path);
  }
}
