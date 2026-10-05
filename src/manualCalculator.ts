import { ComputeFn } from "./resultCache";

export interface RecordedCall<P> {
  readonly id: number;
  readonly input: ArrayBufferView;
  readonly params: P;
  readonly signal: AbortSignal;
}

interface PendingSettlement<R> {
  resolve(value: R): void;
  reject(error: unknown): void;
}

/**
 * A compute function whose results are supplied manually by the test,
 * so timing-sensitive scenarios (late arrivals, aborts, overwrites)
 * are fully deterministic.
 */
export class ManualCalculator<P, R> {
  private nextId = 1;
  private readonly pending = new Map<number, PendingSettlement<R>>();
  readonly calls: RecordedCall<P>[] = [];

  readonly compute: ComputeFn<P, R> = (input, params, signal) => {
    const id = this.nextId++;
    this.calls.push({ id, input, params, signal });
    return new Promise<R>((resolve, reject) => {
      this.pending.set(id, {
        resolve: (value) => {
          this.pending.delete(id);
          resolve(value);
        },
        reject: (error) => {
          this.pending.delete(id);
          reject(error);
        },
      });
    });
  };

  get pendingIds(): number[] {
    return [...this.pending.keys()];
  }

  resolve(id: number, value: R): void {
    const settlement = this.pending.get(id);
    if (settlement === undefined) {
      throw new Error(`no pending call with id ${id}`);
    }
    settlement.resolve(value);
  }

  reject(id: number, error: unknown): void {
    const settlement = this.pending.get(id);
    if (settlement === undefined) {
      throw new Error(`no pending call with id ${id}`);
    }
    settlement.reject(error);
  }

  /** Resolve the oldest still-pending call. */
  resolveOldest(value: R): void {
    const [oldest] = this.pendingIds;
    if (oldest === undefined) {
      throw new Error("no pending calls");
    }
    this.resolve(oldest, value);
  }

  /** Reject the oldest still-pending call. */
  rejectOldest(error: unknown): void {
    const [oldest] = this.pendingIds;
    if (oldest === undefined) {
      throw new Error("no pending calls");
    }
    this.reject(oldest, error);
  }
}
