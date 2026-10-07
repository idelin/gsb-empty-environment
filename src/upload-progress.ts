export interface ProgressSnapshot {
  readonly attemptId: number;
  readonly totalBytes: number;
  readonly chunkCount: number;
  readonly confirmedBytes: number;
  readonly tentativeBytes: number;
  readonly transferredBytes: number;
  readonly confirmedChunks: readonly number[];
  readonly paused: boolean;
  readonly completed: boolean;
}

export class UploadProgressModel {
  readonly totalBytes: number;
  readonly chunkSize: number;

  private readonly chunkCount: number;
  private readonly confirmed: Map<number, number>;
  private readonly tentative: Map<number, number>;
  private attemptId: number;
  private paused: boolean;

  constructor(totalBytes: number, chunkSize: number) {
    if (!Number.isFinite(totalBytes) || totalBytes < 0) {
      throw new RangeError("totalBytes must be a non-negative finite number");
    }
    if (!Number.isInteger(chunkSize) || chunkSize <= 0) {
      throw new RangeError("chunkSize must be a positive integer");
    }
    this.totalBytes = totalBytes;
    this.chunkSize = chunkSize;
    if (totalBytes === 0) {
      this.chunkCount = 0;
    } else {
      this.chunkCount = Math.ceil(totalBytes / chunkSize);
    }
    this.confirmed = new Map<number, number>();
    this.tentative = new Map<number, number>();
    this.attemptId = 0;
    this.paused = false;
  }

  beginAttempt(): number {
    this.tentative.clear();
    this.paused = false;
    this.attemptId += 1;
    return this.attemptId;
  }

  reportProgress(attemptId: number, chunkIndex: number, bytesTransferred: number): void {
    if (!this.isActiveAttempt(attemptId)) {
      return;
    }
    if (!this.isValidChunkIndex(chunkIndex)) {
      return;
    }
    if (this.confirmed.has(chunkIndex)) {
      return;
    }
    const clamped = Math.min(Math.max(0, bytesTransferred), this.chunkBytes(chunkIndex));
    const current = this.tentative.get(chunkIndex) ?? 0;
    if (clamped > current) {
      this.tentative.set(chunkIndex, clamped);
    }
  }

  confirmChunk(attemptId: number, chunkIndex: number, receiptBytes?: number): void {
    if (!this.isActiveAttempt(attemptId)) {
      return;
    }
    if (!this.isValidChunkIndex(chunkIndex)) {
      return;
    }
    if (this.confirmed.has(chunkIndex)) {
      return;
    }
    const expected = this.chunkBytes(chunkIndex);
    let bytes = expected;
    if (receiptBytes !== undefined) {
      bytes = Math.min(Math.max(0, receiptBytes), expected);
    }
    this.confirmed.set(chunkIndex, bytes);
    this.tentative.delete(chunkIndex);
  }

  failAttempt(attemptId: number): void {
    if (attemptId !== this.attemptId) {
      return;
    }
    this.tentative.clear();
  }

  pause(): void {
    this.tentative.clear();
    this.paused = true;
  }

  resume(): number {
    return this.beginAttempt();
  }

  snapshot(): ProgressSnapshot {
    const confirmedBytes = sum(this.confirmed.values());
    const tentativeBytes = sum(this.tentative.values());
    const transferredBytes = Math.min(confirmedBytes + tentativeBytes, this.totalBytes);
    const confirmedChunks = [...this.confirmed.keys()].sort((a, b) => a - b);
    const completed = this.confirmed.size === this.chunkCount;
    return {
      attemptId: this.attemptId,
      totalBytes: this.totalBytes,
      chunkCount: this.chunkCount,
      confirmedBytes,
      tentativeBytes,
      transferredBytes,
      confirmedChunks,
      paused: this.paused,
      completed,
    };
  }

  private isActiveAttempt(attemptId: number): boolean {
    return !this.paused && this.attemptId > 0 && attemptId === this.attemptId;
  }

  private isValidChunkIndex(chunkIndex: number): boolean {
    return Number.isInteger(chunkIndex) && chunkIndex >= 0 && chunkIndex < this.chunkCount;
  }

  private chunkBytes(chunkIndex: number): number {
    const start = chunkIndex * this.chunkSize;
    return Math.min(this.chunkSize, this.totalBytes - start);
  }
}

function sum(values: Iterable<number>): number {
  let total = 0;
  for (const value of values) {
    total += value;
  }
  return total;
}
