import { CancelledError } from './errors';

/**
 * A cooperative cancellation token. Cancellation is only ever *observed*
 * at explicit checkpoints chosen by the executor; awaiting an arbitrary
 * promise never auto-aborts. That is what allows the submit phase to
 * keep waiting for a definitive result after cancellation was requested.
 */
export class CancellationToken {
  private cancelledFlag = false;
  private listeners: Array<() => void> = [];

  get cancelled(): boolean {
    return this.cancelledFlag;
  }

  cancel(): void {
    if (this.cancelledFlag) return;
    this.cancelledFlag = true;
    for (const listener of this.listeners.splice(0)) listener();
  }

  onCancel(listener: () => void): void {
    if (this.cancelledFlag) listener();
    else this.listeners.push(listener);
  }

  /** Only called by the executor at cancellable checkpoints. */
  throwIfCancelled(): void {
    if (this.cancelledFlag) throw new CancelledError();
  }
}
