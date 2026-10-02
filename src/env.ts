import fs from "node:fs";
import path from "node:path";

export function fsyncDir(dir: string): void {
  const fd = fs.openSync(dir, "r");
  try {
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
}

export function atomicWriteFile(dir: string, filename: string, data: string): void {
  const tmp = path.join(dir, `${filename}.tmp`);
  const fd = fs.openSync(tmp, "w");
  try {
    fs.writeSync(fd, data);
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  fs.renameSync(tmp, path.join(dir, filename));
  fsyncDir(dir);
}
