// MANIFEST: append-only log of VersionEdit records.
//
// Record layout (little-endian):
//   +----------------+------------------+-------------------+
//   | length: uint32 | crc32: uint32    | payload: JSON     |
//   +----------------+------------------+-------------------+
// crc32 covers the payload only. A record whose length or checksum
// does not validate is treated as a torn tail (crash mid-append)
// and recovery stops there, discarding it.

import * as fs from 'node:fs';
import { crc32 } from './crc32.ts';
import { encodeEdit, decodeEdit, type VersionEdit } from './versionEdit.ts';

const HEADER_SIZE = 8;

export class ManifestWriter {
  private fd: number;

  constructor(path: string) {
    this.fd = fs.openSync(path, 'a');
  }

  // Append one edit and fsync. After this returns, the edit is durable
  // and recovery is guaranteed to replay it (or the whole record is absent).
  append(edit: VersionEdit): void {
    const payload = encodeEdit(edit);
    const header = Buffer.alloc(HEADER_SIZE);
    header.writeUInt32LE(payload.length, 0);
    header.writeUInt32LE(crc32(payload), 4);
    fs.writeSync(this.fd, header);
    fs.writeSync(this.fd, payload);
    fs.fsyncSync(this.fd);
  }

  close(): void {
    fs.closeSync(this.fd);
  }
}

export interface ReplayResult {
  edits: VersionEdit[];
  validBytes: number;   // bytes consumed by valid records
  fileBytes: number;    // total file size
  hadTornTail: boolean; // true if trailing garbage/partial record was discarded
}

export function replayManifest(path: string): ReplayResult {
  const buf = fs.readFileSync(path);
  const edits: VersionEdit[] = [];
  let offset = 0;
  let hadTornTail = false;

  while (offset < buf.length) {
    if (offset + HEADER_SIZE > buf.length) {
      hadTornTail = true; // partial header
      break;
    }
    const length = buf.readUInt32LE(offset);
    const expectedCrc = buf.readUInt32LE(offset + 4);
    const payloadStart = offset + HEADER_SIZE;
    if (payloadStart + length > buf.length) {
      hadTornTail = true; // partial payload
      break;
    }
    const payload = buf.subarray(payloadStart, payloadStart + length);
    if (crc32(payload) !== expectedCrc) {
      hadTornTail = true; // corrupted payload
      break;
    }
    edits.push(decodeEdit(payload));
    offset = payloadStart + length;
  }

  return { edits, validBytes: offset, fileBytes: buf.length, hadTornTail };
}

// Physically drop the torn tail so future appends start at a clean offset.
export function truncateToValid(path: string, validBytes: number): void {
  fs.truncateSync(path, validBytes);
}
