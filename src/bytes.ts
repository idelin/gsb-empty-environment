import { createHash } from "node:crypto";

export function sha256(...parts: Uint8Array[]): Uint8Array {
  const hash = createHash("sha256");
  for (const part of parts) hash.update(part);
  return new Uint8Array(hash.digest());
}

export function concat(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

export function utf8(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

export function bytesToHex(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("hex");
}

export function hexToBytes(hex: string): Uint8Array {
  if (!/^[0-9a-f]*$/.test(hex) || hex.length % 2 !== 0) {
    throw new Error(`invalid hex string: ${hex.slice(0, 16)}...`);
  }
  return new Uint8Array(Buffer.from(hex, "hex"));
}

export function u64be(value: number, name: string): Uint8Array {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${name} must be a non-negative safe integer, got ${value}`);
  }
  const out = new Uint8Array(8);
  new DataView(out.buffer).setBigUint64(0, BigInt(value), false);
  return out;
}

export function assertLen(bytes: Uint8Array, len: number, name: string): void {
  if (!(bytes instanceof Uint8Array) || bytes.length !== len) {
    throw new Error(
      `${name} must be exactly ${len} bytes, got ${bytes?.length ?? "non-bytes"}`,
    );
  }
}
