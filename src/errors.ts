export class AbortError extends Error {
  constructor(message = 'operation aborted') {
    super(message);
    this.name = 'AbortError';
  }
}

/** 提交已在存储侧生效，但确认回执在回传途中丢失 */
export class ReceiptLostError extends Error {
  readonly operationId: string;
  constructor(operationId: string) {
    super(`confirmation receipt lost after commit: ${operationId}`);
    this.name = 'ReceiptLostError';
    this.operationId = operationId;
  }
}

export class VersionConflictError extends Error {
  readonly path: string;
  readonly currentVersion: number | null;
  constructor(path: string, currentVersion: number | null) {
    super(`version conflict on ${path}: current=${currentVersion ?? '<absent>'}`);
    this.name = 'VersionConflictError';
    this.path = path;
    this.currentVersion = currentVersion;
  }
}

export function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError';
}
