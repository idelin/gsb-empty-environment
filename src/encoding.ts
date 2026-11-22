import { createHash } from 'node:crypto';

export const MAX_SEQ = (1n << 64n) - 1n;
export const ZERO_DIGEST = '0'.repeat(64);

export function concat(parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

export function sha256(...parts: Uint8Array[]): string {
  const h = createHash('sha256');
  for (const p of parts) h.update(p);
  return h.digest('hex');
}

export function utf8(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}

export function u32be(n: number): Uint8Array {
  if (!Number.isInteger(n) || n < 0 || n > 0xffffffff) throw new RangeError(`u32 out of range: ${n}`);
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, n, false);
  return b;
}

export function u64be(n: bigint): Uint8Array {
  if (n < 0n || n > MAX_SEQ) throw new RangeError(`u64 out of range: ${n}`);
  const b = new Uint8Array(8);
  new DataView(b.buffer).setBigUint64(0, n, false);
  return b;
}

export function toHex(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString('hex');
}

export function fromHex(hex: string): Uint8Array {
  return new Uint8Array(Buffer.from(hex, 'hex'));
}

export function taggedBytes(tag: string, ...parts: Uint8Array[]): Uint8Array {
  return concat([utf8(tag), ...parts]);
}

export function lenPrefixed(part: Uint8Array): Uint8Array {
  return concat([u32be(part.length), part]);
}
