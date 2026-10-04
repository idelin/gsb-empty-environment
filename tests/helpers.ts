import { promises as fs } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import type { FileVersionSpec, SegmentInput } from '../src/index.ts';

export async function tempDir(): Promise<string> {
  return fs.mkdtemp(join(tmpdir(), 'reassembly-'));
}

export function sha256(data: Uint8Array | string): string {
  return createHash('sha256').update(data).digest('hex');
}

export function specFor(fileId: string, version: string, content: Buffer): FileVersionSpec {
  return {
    fileId,
    version,
    size: content.length,
    digest: { algorithm: 'sha256', hex: sha256(content) },
  };
}

export function seg(fileId: string, version: string, content: Buffer, start: number, end: number): SegmentInput {
  return { fileId, version, offset: start, data: content.subarray(start, end) };
}

/** Deterministic pseudo-random bytes so tests are reproducible. */
export function content(length: number, seed: number): Buffer {
  const buf = Buffer.alloc(length);
  let state = seed >>> 0;
  for (let i = 0; i < length; i++) {
    state = (state * 1103515245 + 12345) >>> 0;
    buf[i] = (state >>> 16) & 0xff;
  }
  return buf;
}
