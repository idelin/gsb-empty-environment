import fs from "node:fs";
import { crc32 } from "./crc32.ts";
import { VersionEdit } from "./version_edit.ts";

const HEADER_SIZE = 8;

export class ManifestWriter {
  private fd: number;

  constructor(path: string, append: boolean) {
    this.fd = fs.openSync(path, append ? "a" : "w");
  }

  append(edit: VersionEdit): void {
    const payload = edit.encode();
    const header = Buffer.alloc(HEADER_SIZE);
    header.writeUInt32LE(payload.length, 0);
    header.writeUInt32LE(crc32(payload), 4);
    fs.writeSync(this.fd, header);
    fs.writeSync(this.fd, payload);
  }

  sync(): void {
    fs.fsyncSync(this.fd);
  }

  close(): void {
    fs.closeSync(this.fd);
  }
}

export interface ManifestReadResult {
  edits: VersionEdit[];
  validBytes: number;
  corruptedTail: boolean;
}

export function readManifest(path: string): ManifestReadResult {
  const data = fs.readFileSync(path);
  const edits: VersionEdit[] = [];
  let offset = 0;
  let corruptedTail = false;

  while (offset + HEADER_SIZE <= data.length) {
    const length = data.readUInt32LE(offset);
    const expectedCrc = data.readUInt32LE(offset + 4);
    const recordEnd = offset + HEADER_SIZE + length;
    if (recordEnd > data.length) {
      corruptedTail = true;
      break;
    }
    const payload = data.subarray(offset + HEADER_SIZE, recordEnd);
    if (crc32(payload) !== expectedCrc) {
      corruptedTail = true;
      break;
    }
    edits.push(VersionEdit.decode(payload));
    offset = recordEnd;
  }
  if (offset < data.length && offset + HEADER_SIZE > data.length) {
    corruptedTail = true;
  }

  return { edits, validBytes: offset, corruptedTail };
}
