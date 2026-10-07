export interface Deferred<T> {
  readonly promise: Promise<T>;
  resolve(value: T | PromiseLike<T>): void;
  reject(reason?: unknown): void;
  readonly settled: boolean;
}

export function deferred<T>(): Deferred<T> {
  let resolveFn!: (value: T | PromiseLike<T>) => void;
  let rejectFn!: (reason?: unknown) => void;
  let settled = false;
  const promise = new Promise<T>((resolve, reject) => {
    resolveFn = (value) => {
      settled = true;
      resolve(value);
    };
    rejectFn = (reason) => {
      settled = true;
      reject(reason);
    };
  });
  return {
    promise,
    resolve: resolveFn,
    reject: rejectFn,
    get settled() {
      return settled;
    },
  };
}

/**
 * A closable gate. `wait()` blocks until `open()` is called.
 * Tests use it to freeze the importer at an exact async boundary.
 */
export class Gate {
  private slot: Deferred<void> = deferred<void>();
  private opened = false;

  get isOpen(): boolean {
    return this.opened;
  }

  wait(): Promise<void> {
    return this.opened ? Promise.resolve() : this.slot.promise;
  }

  open(): void {
    if (!this.opened) {
      this.opened = true;
      this.slot.resolve();
    }
  }

  reset(): void {
    this.opened = false;
    this.slot = deferred<void>();
  }
}
