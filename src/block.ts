import { decodeVarint64, encodeVarint64 } from './varint.ts';

/**
 * Data block format (LevelDB-compatible):
 *
 *   entry := shared(varint) nonShared(varint) valueLen(varint)
 *            keyDelta[nonShared] value[valueLen]
 *   ...entries...
 *   restarts[numRestarts] : uint32le offsets of restart points
 *   numRestarts           : uint32le
 *
 * Keys are prefix-compressed against the previous key; every
 * RESTART_INTERVAL entries a restart point stores a full key so the
 * reader can binary-search restart points and decode forward.
 */
export const RESTART_INTERVAL = 16;

export class BlockBuilder {
  private chunks: Buffer[] = [];
  private size = 0;
  private restarts: number[] = [0];
  private entriesSinceRestart = 0;
  private lastKey: Buffer = Buffer.alloc(0);
  private count = 0;

  add(key: Buffer, value: Buffer): void {
    let shared = 0;
    if (this.entriesSinceRestart < RESTART_INTERVAL) {
      const limit = Math.min(this.lastKey.length, key.length);
      while (shared < limit && this.lastKey[shared] === key[shared]) shared++;
    } else {
      this.restarts.push(this.size);
      this.entriesSinceRestart = 0;
    }
    const nonShared = key.length - shared;
    const header = Buffer.concat([
      encodeVarint64(shared),
      encodeVarint64(nonShared),
      encodeVarint64(value.length),
    ]);
    this.chunks.push(header, key.subarray(shared), value);
    this.size += header.length + nonShared + value.length;
    this.lastKey = Buffer.from(key); // own a copy; caller may reuse buffers
    this.entriesSinceRestart++;
    this.count++;
  }

  get entryCount(): number {
    return this.count;
  }

  get empty(): boolean {
    return this.count === 0;
  }

  /** Estimated on-disk size including the restart trailer. */
  get currentSize(): number {
    return this.size + this.restarts.length * 4 + 4;
  }

  finish(): Buffer {
    const trailer = Buffer.allocUnsafe(this.restarts.length * 4 + 4);
    for (let i = 0; i < this.restarts.length; i++) {
      trailer.writeUInt32LE(this.restarts[i], i * 4);
    }
    trailer.writeUInt32LE(this.restarts.length, this.restarts.length * 4);
    return Buffer.concat([...this.chunks, trailer]);
  }
}

export class BlockReader {
  private readonly numRestarts: number;
  private readonly restartsOffset: number;
  readonly data: Buffer;

  constructor(data: Buffer) {
    this.data = data;
    if (data.length < 4) throw new Error('corrupt block: too small');
    this.numRestarts = data.readUInt32LE(data.length - 4);
    this.restartsOffset = data.length - 4 - this.numRestarts * 4;
    if (this.restartsOffset < 0) throw new Error('corrupt block: bad restarts');
  }

  iterator(): BlockIterator {
    return new BlockIterator(this.data, this.restartsOffset, this.numRestarts);
  }
}

export class BlockIterator {
  private offset = -1;
  private keyBuf = Buffer.allocUnsafe(256);
  private keyLen = 0;
  private valueView: Buffer = Buffer.alloc(0);
  private readonly data: Buffer;
  private readonly restartsOffset: number;
  private readonly numRestarts: number;

  constructor(data: Buffer, restartsOffset: number, numRestarts: number) {
    this.data = data;
    this.restartsOffset = restartsOffset;
    this.numRestarts = numRestarts;
  }

  private restartOffset(i: number): number {
    return this.data.readUInt32LE(this.restartsOffset + i * 4);
  }

  private keyAt(offset: number): Buffer {
    const shared = decodeVarint64(this.data, offset);
    const nonShared = decodeVarint64(this.data, offset + shared.bytesRead);
    const valueLen = decodeVarint64(
      this.data,
      offset + shared.bytesRead + nonShared.bytesRead,
    );
    const keyStart = offset + shared.bytesRead + nonShared.bytesRead + valueLen.bytesRead;
    return this.data.subarray(keyStart, keyStart + nonShared.value);
  }

  /** Position at the first entry whose key >= target. */
  seek(target: Buffer): void {
    // Binary search restart points: find the last restart with key < target,
    // then decode forward from there.
    let left = 0;
    let right = this.numRestarts - 1;
    while (left < right) {
      const mid = (left + right + 1) >> 1;
      const midKey = this.keyAt(this.restartOffset(mid));
      if (Buffer.compare(midKey, target) < 0) left = mid;
      else right = mid - 1;
    }
    this.seekToRestart(left);
    while (this.valid() && Buffer.compare(this.keyBuf.subarray(0, this.keyLen), target) < 0) {
      this.next();
    }
  }

  seekToFirst(): void {
    this.seekToRestart(0);
  }

  private seekToRestart(index: number): void {
    this.offset = this.restartOffset(index);
    this.keyLen = 0;
    this.parseEntry();
  }

  valid(): boolean {
    return this.offset >= 0 && this.offset < this.restartsOffset;
  }

  /** Current key. View valid only until the next call to next()/seek(). */
  key(): Buffer {
    return this.keyBuf.subarray(0, this.keyLen);
  }

  /** Current value: zero-copy view into the block buffer. */
  value(): Buffer {
    return this.valueView;
  }

  next(): void {
    if (!this.valid()) return;
    this.offset += this.entryLengthAt(this.offset);
    this.parseEntry();
  }

  private entryLengthAt(offset: number): number {
    const shared = decodeVarint64(this.data, offset);
    const nonShared = decodeVarint64(this.data, offset + shared.bytesRead);
    const valueLen = decodeVarint64(
      this.data,
      offset + shared.bytesRead + nonShared.bytesRead,
    );
    return (
      shared.bytesRead + nonShared.bytesRead + valueLen.bytesRead +
      nonShared.value + valueLen.value
    );
  }

  private parseEntry(): void {
    if (!this.valid()) return;
    const shared = decodeVarint64(this.data, this.offset);
    const nonShared = decodeVarint64(this.data, this.offset + shared.bytesRead);
    const valueLen = decodeVarint64(
      this.data,
      this.offset + shared.bytesRead + nonShared.bytesRead,
    );
    const headerLen = shared.bytesRead + nonShared.bytesRead + valueLen.bytesRead;
    const keyStart = this.offset + headerLen;
    const totalKeyLen = shared.value + nonShared.value;
    if (this.keyBuf.length < totalKeyLen) {
      const grown = Buffer.allocUnsafe(Math.max(totalKeyLen, this.keyBuf.length * 2));
      this.keyBuf.copy(grown, 0, 0, this.keyLen);
      this.keyBuf = grown;
    }
    this.data.copy(this.keyBuf, shared.value, keyStart, keyStart + nonShared.value);
    this.keyLen = totalKeyLen;
    const valueStart = keyStart + nonShared.value;
    this.valueView = this.data.subarray(valueStart, valueStart + valueLen.value);
  }
}
