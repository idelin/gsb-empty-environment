/**
 * Chunked upload progress tracker.
 *
 * Progress is split into two kinds of values:
 * - Confirmed bytes: only advance when the server acknowledges a chunk via a
 *   completion receipt. They are reliable, never roll back, and each chunk is
 *   counted at most once (duplicate receipts are ignored).
 * - Temporary (in-flight) bytes: optimistic transfer progress reported by the
 *   client while a chunk is being sent. They may be rolled back on retry and
 *   are discarded when an attempt ends or the upload is paused.
 *
 * Every event is tagged with an attempt id. Events from old attempts are
 * ignored, so late arrivals from a failed/paused attempt cannot corrupt the
 * current state. The confirmed set survives pause/resume.
 */

export interface ProgressSnapshot {
  /** Total size of the file in bytes. */
  readonly totalBytes: number;
  /** Reliable progress: bytes of chunks confirmed by completion receipts. */
  readonly confirmedBytes: number;
  /** Temporary progress: in-flight bytes reported but not yet confirmed. */
  readonly temporaryBytes: number;
  /** confirmedBytes / totalBytes, in [0, 1]. */
  readonly reliableRatio: number;
  /** (confirmedBytes + temporaryBytes) / totalBytes, capped at 1. */
  readonly displayRatio: number;
  /** Indices of confirmed chunks, ascending. */
  readonly confirmedChunks: readonly number[];
  /** The current attempt id. */
  readonly attemptId: number;
}

interface ChunkState {
  /** Bytes acknowledged by a completion receipt; 0 until confirmed. */
  confirmed: number;
  /** Attempt id that owns the current temporary value, or 0 if none. */
  inflightAttempt: number;
  /** Temporary bytes reported for `inflightAttempt`. */
  inflightBytes: number;
}

export class ChunkedUploadProgress {
  private readonly totalBytes: number;
  private readonly chunkSize: number;
  private readonly chunks: ChunkState[];
  private confirmedBytes = 0;
  private attempt = 0;

  constructor(totalBytes: number, chunkSize: number) {
    if (!Number.isFinite(totalBytes) || totalBytes <= 0) {
      throw new RangeError('totalBytes must be a positive finite number');
    }
    if (!Number.isFinite(chunkSize) || chunkSize <= 0) {
      throw new RangeError('chunkSize must be a positive finite number');
    }
    this.totalBytes = Math.floor(totalBytes);
    this.chunkSize = Math.floor(chunkSize);
    const count = Math.ceil(this.totalBytes / this.chunkSize);
    this.chunks = Array.from({ length: count }, () => ({
      confirmed: 0,
      inflightAttempt: 0,
      inflightBytes: 0,
    }));
  }

  get chunkCount(): number {
    return this.chunks.length;
  }

  /** Size in bytes of the given chunk (the last chunk may be smaller). */
  chunkSizeOf(index: number): number {
    this.assertValidIndex(index);
    const start = index * this.chunkSize;
    return Math.min(this.chunkSize, this.totalBytes - start);
  }

  /**
   * Start a new upload attempt. Temporary values from the previous attempt
   * are rolled back; confirmed bytes are preserved. Returns the new attempt
   * id which must tag all subsequent events for this attempt.
   */
  beginAttempt(): number {
    this.attempt += 1;
    for (const chunk of this.chunks) {
      chunk.inflightAttempt = 0;
      chunk.inflightBytes = 0;
    }
    return this.attempt;
  }

  /**
   * Pause the upload. The confirmed set is preserved; temporary values are
   * discarded and any late events from in-flight requests are ignored
   * (the attempt id is invalidated).
   */
  pause(): void {
    this.beginAttempt();
  }

  /** Resume after a pause. Returns the attempt id to tag new events with. */
  resume(): number {
    return this.beginAttempt();
  }

  /**
   * Report temporary transfer progress for a chunk. Out-of-order reports
   * within the same attempt never decrease the value (max wins), and the
   * value is capped at the chunk size. Events from old attempts and for
   * already-confirmed chunks are ignored.
   */
  reportTransfer(chunkIndex: number, attemptId: number, bytesSent: number): boolean {
    this.assertValidIndex(chunkIndex);
    const chunk = this.chunks[chunkIndex]!;
    if (attemptId !== this.attempt || chunk.confirmed > 0) {
      return false;
    }
    if (chunk.inflightAttempt !== attemptId) {
      chunk.inflightAttempt = attemptId;
      chunk.inflightBytes = 0;
    }
    const capped = Math.min(Math.max(0, bytesSent), this.chunkSizeOf(chunkIndex));
    chunk.inflightBytes = Math.max(chunk.inflightBytes, capped);
    return true;
  }

  /**
   * Apply a completion receipt for a chunk. Idempotent: duplicate receipts
   * for an already-confirmed chunk are ignored and bytes are never counted
   * twice. Receipts from old attempts are ignored.
   */
  confirmChunk(chunkIndex: number, attemptId: number): boolean {
    this.assertValidIndex(chunkIndex);
    const chunk = this.chunks[chunkIndex]!;
    if (attemptId !== this.attempt || chunk.confirmed > 0) {
      return false;
    }
    chunk.confirmed = this.chunkSizeOf(chunkIndex);
    chunk.inflightAttempt = 0;
    chunk.inflightBytes = 0;
    this.confirmedBytes += chunk.confirmed;
    return true;
  }

  /**
   * Roll back all temporary values of the given attempt (e.g. after a
   * network failure triggers a retry). Confirmed bytes are never rolled
   * back. Rolling back a non-current attempt is a no-op because its
   * temporary values were already discarded.
   */
  rollbackAttempt(attemptId: number): void {
    if (attemptId !== this.attempt) {
      return;
    }
    for (const chunk of this.chunks) {
      if (chunk.inflightAttempt === attemptId) {
        chunk.inflightAttempt = 0;
        chunk.inflightBytes = 0;
      }
    }
  }

  snapshot(): ProgressSnapshot {
    let temporaryBytes = 0;
    const confirmedChunks: number[] = [];
    for (let i = 0; i < this.chunks.length; i += 1) {
      const chunk = this.chunks[i]!;
      if (chunk.confirmed > 0) {
        confirmedChunks.push(i);
      } else {
        temporaryBytes += chunk.inflightBytes;
      }
    }
    return {
      totalBytes: this.totalBytes,
      confirmedBytes: this.confirmedBytes,
      temporaryBytes,
      reliableRatio: this.confirmedBytes / this.totalBytes,
      displayRatio: Math.min(1, (this.confirmedBytes + temporaryBytes) / this.totalBytes),
      confirmedChunks,
      attemptId: this.attempt,
    };
  }

  private assertValidIndex(index: number): void {
    if (!Number.isInteger(index) || index < 0 || index >= this.chunks.length) {
      throw new RangeError(`chunk index ${index} out of range [0, ${this.chunks.length})`);
    }
  }
}
