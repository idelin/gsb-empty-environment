import { readFileSync } from 'node:fs';
import { CodecError } from '../codec/errors.ts';
import { tryDecodeFrame, type FrameView } from '../codec/frame.ts';
import { resources } from './resources.ts';

export interface ScannedFrame extends FrameView {
  offset: number;
}

/**
 * Memory-mapped style segment reader. Node's stdlib has no mmap(2) binding,
 * so the whole segment is loaded once into a single shared, read-only
 * Buffer that plays the role of the mapping: every record parsed from it is
 * a zero-copy view (Buffer.subarray) into that mapping, never a copy.
 */
export class MappedSegment {
  readonly path: string;
  readonly buffer: Buffer;

  private constructor(path: string, buffer: Buffer) {
    this.path = path;
    this.buffer = buffer;
    resources.open('segment');
  }

  static map(path: string): MappedSegment {
    return new MappedSegment(path, readFileSync(path));
  }

  static fromBuffer(buffer: Buffer, path = '<memory>'): MappedSegment {
    return new MappedSegment(path, buffer);
  }

  #closed = false;

  get closed(): boolean {
    return this.#closed;
  }

  #assertOpen(): void {
    if (this.#closed) throw new CodecError('MALFORMED', `segment ${this.path} is closed`);
  }

  /**
   * Lazily scan frames. Stops cleanly at a truncated tail (crash during
   * append) and records the offset in `truncatedAt`.
   */
  *scan(startOffset = 0): Generator<ScannedFrame> {
    this.#assertOpen();
    let offset = startOffset;
    this.truncatedAt = -1;
    while (offset < this.buffer.length) {
      let frame: FrameView | null;
      try {
        frame = tryDecodeFrame(this.buffer, offset);
      } catch (err) {
        if (err instanceof CodecError && err.code === 'TRUNCATED') {
          this.truncatedAt = offset;
          return;
        }
        throw err;
      }
      if (frame === null) {
        this.truncatedAt = offset;
        return;
      }
      yield { offset, ...frame };
      offset = frame.nextOffset;
    }
  }

  /** Offset where a torn tail begins, or -1 when the segment is clean. */
  truncatedAt = -1;

  /** Decode the frame at an exact offset (zero-copy payload view). */
  at(offset: number): FrameView {
    this.#assertOpen();
    const frame = tryDecodeFrame(this.buffer, offset);
    if (frame === null) {
      throw new CodecError('TRUNCATED', `no complete frame at offset ${offset} in ${this.path}`);
    }
    return frame;
  }

  close(): void {
    if (!this.#closed) {
      this.#closed = true;
      resources.close('segment');
    }
  }
}
