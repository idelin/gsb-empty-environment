import { createHash } from "crypto";

export function sha256Hex(data: Buffer | string): string {
  return createHash("sha256").update(data).digest("hex");
}

export function sha256Raw(data: Buffer | string): Buffer {
  return createHash("sha256").update(data).digest();
}
