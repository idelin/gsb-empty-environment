export interface CancellationToken {
  readonly cancelled: boolean;
  onCancel(listener: () => void): void;
}

export class CancellationTokenSource {
  private listeners: Array<() => void> = [];
  private isCancelled = false;

  readonly token: CancellationToken;

  constructor() {
    const source = this;
    this.token = {
      get cancelled(): boolean {
        return source.isCancelled;
      },
      onCancel(listener: () => void): void {
        if (source.isCancelled) {
          listener();
        } else {
          source.listeners.push(listener);
        }
      },
    };
  }

  cancel(): void {
    if (this.isCancelled) return;
    this.isCancelled = true;
    const listeners = this.listeners;
    this.listeners = [];
    for (const listener of listeners) listener();
  }
}
