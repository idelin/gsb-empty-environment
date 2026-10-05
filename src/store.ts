export interface FileVersion {
  readonly path: string;
  readonly version: number;
  readonly digest: string;
  /** 产生该版本的操作幂等键，用于回执丢失后的对账 */
  readonly operationId: string;
  readonly committedAt: number;
}

export interface VersionStore {
  get(path: string): FileVersion | undefined;
  /**
   * 乐观并发控制：仅当当前版本与 expectedVersion 一致时写入新版本，
   * 返回新版本记录；冲突时返回 undefined。
   */
  compareAndSwap(
    path: string,
    expectedVersion: number | null,
    digest: string,
    operationId: string,
  ): FileVersion | undefined;
  findByOperationId(operationId: string): FileVersion | undefined;
}

export class InMemoryVersionStore implements VersionStore {
  private readonly files = new Map<string, FileVersion>();
  private readonly now: () => number;

  constructor(now: () => number = Date.now) {
    this.now = now;
  }

  get(path: string): FileVersion | undefined {
    return this.files.get(path);
  }

  compareAndSwap(
    path: string,
    expectedVersion: number | null,
    digest: string,
    operationId: string,
  ): FileVersion | undefined {
    const current = this.files.get(path);
    const matches =
      expectedVersion === null ? current === undefined : current?.version === expectedVersion;
    if (!matches) {
      return undefined;
    }
    const next: FileVersion = {
      path,
      version: (current?.version ?? 0) + 1,
      digest,
      operationId,
      committedAt: this.now(),
    };
    this.files.set(path, next);
    return next;
  }

  findByOperationId(operationId: string): FileVersion | undefined {
    for (const file of this.files.values()) {
      if (file.operationId === operationId) {
        return file;
      }
    }
    return undefined;
  }
}
