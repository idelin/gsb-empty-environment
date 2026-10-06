export interface CancellationToken {
  readonly isCancellationRequested: boolean;
}

/**
 * Cooperative cancellation. The commit loop only observes the token between
 * batches: a batch that has already started always runs to completion and
 * reports its real result.
 */
export class CancellationSource {
  private requested = false;
  readonly token: CancellationToken;

  constructor() {
    const self = this;
    this.token = {
      get isCancellationRequested(): boolean {
        return self.requested;
      },
    };
  }

  cancel(): void {
    this.requested = true;
  }
}
