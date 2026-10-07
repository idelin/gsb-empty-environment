export class CancelledError extends Error {
  constructor(message = 'operation cancelled') {
    super(message);
    this.name = 'CancelledError';
  }
}

/**
 * Cooperative cancellation. The importer checks the token only at batch
 * boundaries: a batch whose commit has already started always runs to
 * completion and reports its real result.
 */
export class CancellationToken {
  private listeners: Array<() => void> = [];
  private cancelledFlag = false;

  get cancelled(): boolean {
    return this.cancelledFlag;
  }

  cancel(): void {
    if (this.cancelledFlag) {
      return;
    }
    this.cancelledFlag = true;
    const listeners = this.listeners.splice(0);
    for (const listener of listeners) {
      listener();
    }
  }

  onCancel(listener: () => void): void {
    if (this.cancelledFlag) {
      listener();
    } else {
      this.listeners.push(listener);
    }
  }

  throwIfCancelled(): void {
    if (this.cancelledFlag) {
      throw new CancelledError();
    }
  }
}
