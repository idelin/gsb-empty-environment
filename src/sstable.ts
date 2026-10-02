import fs from "node:fs";
import path from "node:path";
import { crc32 } from "./crc32.ts";
import { fsyncDir } from "./env.ts";
import type { FileMetaData } from "./version_edit.ts";

export type Entry = [string, string];

export function sstableFileName(fileNumber: number): string {
  return `${String(fileNumber).padStart(6, "0")}.sst`;
}

export function writeSSTable(dir: string, fileNumber: number, entries: Entry[]): FileMetaData {
  const sorted = [...entries].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  const body = Buffer.from(JSON.stringify(sorted), "utf8");
  const checksum = crc32(body);
  const trailer = Buffer.alloc(4);
  trailer.writeUInt32LE(checksum, 0);

  const filePath = path.join(dir, sstableFileName(fileNumber));
  const fd = fs.openSync(filePath, "w");
  try {
    fs.writeSync(fd, body);
    fs.writeSync(fd, trailer);
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  fsyncDir(dir);

  return {
    fileNumber,
    fileSize: body.length + 4,
    smallestKey: sorted.length > 0 ? sorted[0][0] : "",
    largestKey: sorted.length > 0 ? sorted[sorted.length - 1][0] : "",
    checksum,
  };
}

export function verifySSTable(dir: string, meta: FileMetaData): boolean {
  const filePath = path.join(dir, sstableFileName(meta.fileNumber));
  let data: Buffer;
  try {
    data = fs.readFileSync(filePath);
  } catch {
    return false;
  }
  if (data.length !== meta.fileSize || data.length < 4) return false;
  const body = data.subarray(0, data.length - 4);
  const storedCrc = data.readUInt32LE(data.length - 4);
  return crc32(body) === storedCrc && storedCrc === meta.checksum;
}

export function readSSTable(dir: string, fileNumber: number): Entry[] {
  const data = fs.readFileSync(path.join(dir, sstableFileName(fileNumber)));
  return JSON.parse(data.subarray(0, data.length - 4).toString("utf8")) as Entry[];
}
