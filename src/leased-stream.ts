import { ReadAbortedError, ReadClosedError } from "./errors.ts";
import type { Clock, LeaseEndDetail, LeaseEndReason } from "./types.ts";

export interface LeasedStreamOptions {
  readonly token: string;
  readonly data: Uint8Array;
  readonly absoluteExpiry: number;
  readonly chunkSize: number;
  readonly clock: Clock;
  /** Optional caller signal; aborting it cancels the stream. */
  readonly signal?: AbortSignal;
  onEnd: (detail: LeaseEndDetail) => void;
}

export type LeaseStreamState = "open" | "complete" | "expired" | "cancelled";

/**
 * A read handle whose lifetime is bounded by a fixed lease.
 *
 * The lease is recorded on the stream when it is issued and runs to its
 * natural end regardless of what later happens to the address (for example
 * revocation or document navigation). A stream ends for exactly one of
 * three reasons: full consumption (`complete`), lease expiry (`expired`)
 * or explicit cancellation (`cancelled`).
 */
export class LeasedReadStream {
  readonly token: string;
  readonly issuedAt: number;
  readonly absoluteExpiry: number;
  readonly chunkSize: number;

  private readonly data: Uint8Array;
  private readonly clock: Clock;
  private readonly endCallback: (detail: LeaseEndDetail) => void;
  private offset = 0;
  private state: LeaseStreamState = "open";
  private endDetail: LeaseEndDetail | null = null;
  private readonly waiters: Array<() => void> = [];

  constructor(options: LeasedStreamOptions) {
    this.token = options.token;
    this.data = options.data;
    this.absoluteExpiry = options.absoluteExpiry;
    this.chunkSize = options.chunkSize;
    this.clock = options.clock;
    this.issuedAt = options.clock.now();
    this.endCallback = options.onEnd;

    options.signal?.addEventListener(
      "abort",
      () => {
        this.finish("cancelled");
      },
      { once: true },
    );

    // Defer so construction (and registry bookkeeping) finishes before
    // the end callback runs.
    if (this.data.byteLength === 0) {
      queueMicrotask(() => this.finish("complete"));
    }
  }

  get leaseState(): LeaseStreamState {
    return this.state;
  }

  get bytesRead(): number {
    return this.offset;
  }

  get remainingMs(): number {
    return Math.max(0, this.absoluteExpiry - this.clock.now());
  }

  get active(): boolean {
    return this.state === "open" && this.clock.now() < this.absoluteExpiry;
  }

  /**
   * Read the next chunk. Resolves to null at end of data. Throws once the
   * stream has ended; a lease whose clock has passed expiry ends the stream.
   */
  async read(): Promise<Uint8Array | null> {
    this.throwIfClosed();
    if (this.clock.now() >= this.absoluteExpiry) {
      this.finish("expired");
      throw new ReadClosedError("blob read lease has expired");
    }
    if (this.offset >= this.data.byteLength) {
      this.finish("complete");
      return null;
    }

    const end = Math.min(
      this.offset + this.chunkSize,
      this.data.byteLength,
    );
    const chunk = this.data.subarray(this.offset, end);
    this.offset = end;

    if (this.offset >= this.data.byteLength) {
      this.finish("complete");
    }
    return chunk;
  }

  /** Release the lease early. Idempotent and safe after natural end. */
  cancel(reason: LeaseEndReason = "cancelled"): void {
    if (this.state !== "open") {
      return;
    }
    this.finish(reason);
  }

  /** Resolves when the stream ends for any reason. */
  waitForLeaseEnd(): Promise<LeaseEndDetail> {
    if (this.endDetail) {
      return Promise.resolve(this.endDetail);
    }
    return new Promise<LeaseEndDetail>((resolve) => {
      this.waiters.push(() => resolve(this.endDetail as LeaseEndDetail));
    });
  }

  [Symbol.asyncIterator](): AsyncIterableIterator<Uint8Array> {
    return {
      next: async (): Promise<IteratorResult<Uint8Array>> => {
        try {
          const chunk = await this.read();
          if (chunk === null) {
            return { value: undefined, done: true };
          }
          return { value: chunk, done: false };
        } catch (error) {
          if (
            error instanceof ReadClosedError ||
            error instanceof ReadAbortedError
          ) {
            return { value: undefined, done: true };
          }
          throw error;
        }
      },
      return: async (): Promise<IteratorResult<Uint8Array>> => {
        this.cancel();
        return { value: undefined, done: true };
      },
      [Symbol.asyncIterator](): AsyncIterableIterator<Uint8Array> {
        return this;
      },
    };
  }

  private throwIfClosed(): void {
    if (this.state === "open") {
      return;
    }
    if (this.state === "complete") {
      return;
    }
    if (this.state === "expired") {
      throw new ReadClosedError("blob read lease has expired");
    }
    if (this.state === "cancelled") {
      throw new ReadAbortedError("blob read stream was cancelled");
    }
    throw new ReadClosedError("blob read stream is complete");
  }

  private finish(reason: LeaseEndReason): void {
    if (this.state !== "open") {
      return;
    }
    this.state = reason;
    this.endDetail = {
      token: this.token,
      reason,
      endedAt: this.clock.now(),
      bytesRead: this.offset,
    };
    const waiters = this.waiters.splice(0);
    for (const wake of waiters) {
      wake();
    }
    this.endCallback(this.endDetail);
  }
}
