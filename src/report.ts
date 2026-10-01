import type { ValidationError } from './types.ts';

export interface SpaceReport {
  errorCount: number;
  rawBytes: number;
  compressedBytes: number;
  savedBytes: number;
  compressedRatio: number;
}

const byteLength = (s: string): number => new TextEncoder().encode(s).length;

/** Measures the actual space saving of the compressed document vs. the raw error list. */
export function spaceReport(errors: ValidationError[], document: unknown): SpaceReport {
  const rawBytes = byteLength(JSON.stringify(errors));
  const compressedBytes = byteLength(JSON.stringify(document));
  return {
    errorCount: errors.length,
    rawBytes,
    compressedBytes,
    savedBytes: rawBytes - compressedBytes,
    compressedRatio: rawBytes === 0 ? 1 : compressedBytes / rawBytes,
  };
}
