export class ReleasedBatchError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ReleasedBatchError';
  }
}

export class StaleSelectionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'StaleSelectionError';
  }
}

export class QueryAbortedError extends Error {
  constructor(message = 'query aborted') {
    super(message);
    this.name = 'QueryAbortedError';
  }
}

export function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) {
    throw new QueryAbortedError();
  }
}
