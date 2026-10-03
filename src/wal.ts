import { close as closeCb, closeSync, fsync as fsyncCb, fsyncSync, openSync, readFileSync, writeSync } from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { crc32 } from './crc32.ts';

const fsyncAsync = promisify(fsyncCb);
const closeFdAsync = promisify(closeCb);

export const WAL_OP_PUT = 1;
export const WAL_OP_DELETE = 2;

// record layout: crc32(4) | op(1) | keyLen(4) | valueLen(4) | key | value
// crc covers op..value so torn/corrupt tails are detected on replay.
const HEADER_BYTES = 13;

export function segmentName(seq: number): string {
  return seq.toString().padStart(6, '0') + '.log';
}

export class WalSegmentWriter {
  readonly filePath: string;
  private readonly fd: number;

  constructor(dir: string, seq: number) {
    this.filePath = path.join(dir, segmentName(seq));
    this.fd = openSync(this.filePath, 'a');
  }

  appendPut(key: string, value: Buffer): void {
    this.append(WAL_OP_PUT, key, value);
  }

  appendDelete(key: string): void {
    this.append(WAL_OP_DELETE, key, Buffer.alloc(0));
  }

  private append(op: number, key: string, value: Buffer): void {
    const keyBuf = Buffer.from(key);
    const header = Buffer.allocUnsafe(HEADER_BYTES);
    header.writeUInt8(op, 4);
    header.writeUInt32LE(keyBuf.length, 5);
    header.writeUInt32LE(value.length, 9);
    const crc = crc32(Buffer.concat([header.subarray(4), keyBuf, value]));
    header.writeUInt32LE(crc, 0);
    writeSync(this.fd, Buffer.concat([header, keyBuf, value]));
  }

  sync(): void {
    fsyncSync(this.fd);
  }

  // Background-friendly close: fsync + close without blocking the write path.
  async closeAsync(): Promise<void> {
    try {
      await fsyncAsync(this.fd);
    } finally {
      await closeFdAsync(this.fd);
    }
  }

  close(): void {
    try {
      fsyncSync(this.fd);
    } finally {
      closeSync(this.fd);
    }
  }
}

export function replaySegment(
  filePath: string,
  apply: (op: number, key: string, value: Buffer) => void,
): number {
  const buf = readFileSync(filePath);
  let offset = 0;
  let count = 0;
  while (offset + HEADER_BYTES <= buf.length) {
    const crc = buf.readUInt32LE(offset);
    const op = buf.readUInt8(offset + 4);
    const keyLen = buf.readUInt32LE(offset + 5);
    const valueLen = buf.readUInt32LE(offset + 9);
    const end = offset + HEADER_BYTES + keyLen + valueLen;
    if (end > buf.length) break; // torn write at tail
    const body = Buffer.concat([
      buf.subarray(offset + 4, offset + HEADER_BYTES),
      buf.subarray(offset + HEADER_BYTES, end),
    ]);
    if (crc32(body) !== crc) break; // corrupt tail, stop replay
    apply(
      op,
      buf.toString('utf8', offset + HEADER_BYTES, offset + HEADER_BYTES + keyLen),
      buf.subarray(offset + HEADER_BYTES + keyLen, end),
    );
    count++;
    offset = end;
  }
  return count;
}
