import { VersionConflictError } from './types.ts';

export interface FileState {
  version: number;
  digest: string;
}

/** The target system being published to. Implementations must be atomic per path. */
export interface FileStore {
  read(path: string): Promise<FileState | undefined>;
  /** Atomic compare-and-swap: commits only if current version === expectedVersion. */
  compareAndSwap(path: string, expectedVersion: number, digest: string): Promise<FileState>;
}

/** In-memory reference implementation. */
export class MemoryFileStore implements FileStore {
  private files = new Map<string, FileState>();
  /** Observability hook for tests: counts successful commits. */
  commitCount = 0;

  seed(path: string, state: FileState): void {
    this.files.set(path, { ...state });
  }

  async read(path: string): Promise<FileState | undefined> {
    const state = this.files.get(path);
    return state ? { ...state } : undefined;
  }

  async compareAndSwap(path: string, expectedVersion: number, digest: string): Promise<FileState> {
    const current = this.files.get(path);
    const currentVersion = current?.version;
    if (currentVersion !== expectedVersion) {
      throw new VersionConflictError(path, currentVersion);
    }
    const next: FileState = { version: expectedVersion + 1, digest };
    this.files.set(path, next);
    this.commitCount += 1;
    return { ...next };
  }
}
