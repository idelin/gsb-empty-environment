import {
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readFileSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import { dirname } from 'node:path';

/**
 * Durable committed key-value store. Updates are applied atomically via
 * write-tmp + fsync + rename + fsync(dir), so readers never observe a
 * partially written file.
 */
export class Store {
  private data: Record<string, unknown>;
  readonly file: string;

  constructor(file: string) {
    this.file = file;
    mkdirSync(dirname(file), { recursive: true });
    this.data = existsSync(file)
      ? (JSON.parse(readFileSync(file, 'utf8')) as Record<string, unknown>)
      : {};
  }

  get(key: string): unknown {
    return this.data[key];
  }

  entries(): Record<string, unknown> {
    return { ...this.data };
  }

  /** Applies writes atomically and durably. A `null` value deletes the key. */
  apply(writes: Record<string, unknown>): void {
    for (const [key, value] of Object.entries(writes)) {
      if (value === null) delete this.data[key];
      else this.data[key] = value;
    }
    const tmp = this.file + '.tmp';
    writeFileSync(tmp, JSON.stringify(this.data, null, 2));
    const fd = openSync(tmp, 'r');
    fsyncSync(fd);
    closeSync(fd);
    renameSync(tmp, this.file);
    const dirFd = openSync(dirname(this.file), 'r');
    fsyncSync(dirFd);
    closeSync(dirFd);
  }
}
