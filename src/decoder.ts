// Incremental streaming decoder. Accepts input in arbitrary byte-sized
// chunks, hands output out under a caller-supplied quota, and never emits a
// byte twice. A block's output is released only after its CRC verifies, so a
// corrupt block never reaches the caller; recover() skips a broken block and
// resumes at the next independent block.

import { BlockError, DEFAULT_MAX_BLOCK_SIZE, concatBytes } from './format.ts';
import { decodeBlock } from './block.ts';

export interface StreamDecoderOptions {
  maxBlockSize?: number;
}

export class StreamDecoder {
  private pending: Uint8Array = new Uint8Array(0); // unconsumed input
  private outQueue: Uint8Array[] = []; // verified blocks awaiting read()
  private outOffset = 0;
  private ended = false;
  private skipRemaining = 0;
  private brokenBlockBytes: number | null = null;
  private maxBlockSize: number;
  blockIndex = 0; // index of the block currently being parsed

  constructor(opts: StreamDecoderOptions = {}) {
    this.maxBlockSize = opts.maxBlockSize ?? DEFAULT_MAX_BLOCK_SIZE;
  }

  // Feeds more input. Chunks may be split at any byte boundary.
  push(chunk: Uint8Array): void {
    if (this.ended) throw new Error('push() after end()');
    if (chunk.length === 0) return;
    const next = new Uint8Array(this.pending.length + chunk.length);
    next.set(this.pending, 0);
    next.set(chunk, this.pending.length);
    this.pending = next;
  }

  // Signals that no more input will arrive.
  end(): void {
    this.ended = true;
  }

  // Reads up to maxBytes of decompressed output. Returns fewer (possibly
  // zero) bytes when more input is needed or the stream is exhausted.
  read(maxBytes: number): Uint8Array {
    if (maxBytes < 0) throw new RangeError('maxBytes must be >= 0');
    if (maxBytes === 0) return new Uint8Array(0);
    this.pump();
    const parts: Uint8Array[] = [];
    let remaining = maxBytes;
    while (remaining > 0 && this.outQueue.length > 0) {
      const head = this.outQueue[0];
      const avail = head.length - this.outOffset;
      const take = Math.min(avail, remaining);
      parts.push(head.slice(this.outOffset, this.outOffset + take));
      this.outOffset += take;
      remaining -= take;
      if (this.outOffset === head.length) {
        this.outQueue.shift();
        this.outOffset = 0;
      }
    }
    return parts.length === 1 ? parts[0] : concatBytes(parts);
  }

  // After end(): verifies the stream ended on a block boundary. Throws
  // BlockError('TRUNCATED') otherwise.
  finish(): void {
    this.ended = true;
    this.pump();
    if (this.pending.length !== 0 || this.skipRemaining !== 0) {
      throw new BlockError('TRUNCATED', 'input ends in the middle of a block');
    }
  }

  // After a thrown BlockError: skips the rest of the broken block (its size
  // comes from its header) so subsequent reads continue with the next
  // independent block. Throws if the header itself was unreadable.
  recover(): void {
    if (this.brokenBlockBytes === null) {
      throw new Error('cannot resynchronize: block header was not parsed; use reset()');
    }
    this.skipRemaining = this.brokenBlockBytes;
    this.brokenBlockBytes = null;
    this.blockIndex++;
  }

  reset(): void {
    this.pending = new Uint8Array(0);
    this.outQueue = [];
    this.outOffset = 0;
    this.ended = false;
    this.skipRemaining = 0;
    this.brokenBlockBytes = null;
    this.blockIndex = 0;
  }

  private pump(): void {
    for (;;) {
      if (this.skipRemaining > 0) {
        const drop = Math.min(this.skipRemaining, this.pending.length);
        this.pending = this.pending.subarray(drop);
        this.skipRemaining -= drop;
        if (this.skipRemaining > 0) return;
        continue;
      }
      if (this.pending.length === 0) return;
      let result;
      try {
        result = decodeBlock(this.pending, 0, { maxBlockSize: this.maxBlockSize });
      } catch (e) {
        if (e instanceof BlockError && e.code === 'TRUNCATED' && !this.ended) {
          return; // wait for more input
        }
        if (e instanceof BlockError && e.blockBytes !== null) {
          this.brokenBlockBytes = e.blockBytes;
        }
        throw e;
      }
      this.pending = this.pending.subarray(result.bytesConsumed);
      this.outQueue.push(result.data);
      this.blockIndex++;
    }
  }
}
