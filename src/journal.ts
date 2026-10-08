import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import type { PersistedState } from "./types.ts";

export interface Journal {
  load(): Promise<PersistedState | null>;
  save(state: PersistedState): Promise<void>;
}

export class MemoryJournal implements Journal {
  state: PersistedState | null = null;

  load(): Promise<PersistedState | null> {
    return Promise.resolve(this.state ? structuredClone(this.state) : null);
  }

  save(state: PersistedState): Promise<void> {
    this.state = structuredClone(state);
    return Promise.resolve();
  }
}

export class FileJournal implements Journal {
  readonly path: string;

  constructor(path: string) {
    this.path = path;
  }

  async load(): Promise<PersistedState | null> {
    try {
      return JSON.parse(await readFile(this.path, "utf8")) as PersistedState;
    } catch (err) {
      if ((err as { code?: string }).code === "ENOENT") return null;
      throw err;
    }
  }

  async save(state: PersistedState): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const tmp = `${this.path}.tmp`;
    await writeFile(tmp, JSON.stringify(state, null, 2));
    await rename(tmp, this.path);
  }
}
