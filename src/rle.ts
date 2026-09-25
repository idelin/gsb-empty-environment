import type { Run } from "./types.ts";

export class DecodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DecodeError";
  }
}

export class LabelError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "LabelError";
  }
}

export function assertValidLabel(label: number, labelCount: number): void {
  if (!Number.isInteger(label) || label < 0 || label >= labelCount) {
    throw new LabelError(`invalid label ${label} (allowed 0..${labelCount - 1})`);
  }
}

export function runsLength(runs: Run[]): number {
  let total = 0;
  for (const run of runs) total += run.length;
  return total;
}

export function validateRuns(runs: Run[], width: number, labelCount: number): void {
  let total = 0;
  let prev = -1;
  for (const run of runs) {
    if (!Number.isInteger(run.length) || run.length <= 0) {
      throw new DecodeError(`run length must be a positive integer, got ${run.length}`);
    }
    assertValidLabel(run.label, labelCount);
    if (run.label === prev) {
      throw new DecodeError(`adjacent runs share label ${run.label}; runs must be merged`);
    }
    total += run.length;
    if (total > width) {
      throw new DecodeError(`runs decode to more than ${width} pixels`);
    }
    prev = run.label;
  }
  if (total !== width) {
    throw new DecodeError(`runs decode to ${total} pixels, expected ${width}`);
  }
}

export function decodeRuns(runs: Run[], width: number, labelCount: number): number[] {
  validateRuns(runs, width, labelCount);
  const out = new Array<number>(width);
  let pos = 0;
  for (const run of runs) {
    out.fill(run.label, pos, pos + run.length);
    pos += run.length;
  }
  return out;
}

export function normalizeRuns(runs: Run[]): Run[] {
  const out: Run[] = [];
  for (const run of runs) {
    if (run.length <= 0) continue;
    const last = out[out.length - 1];
    if (last && last.label === run.label) last.length += run.length;
    else out.push({ label: run.label, length: run.length });
  }
  return out;
}

export function rowDataToRuns(
  data: ArrayLike<number>,
  offset: number,
  width: number,
  labelCount: number,
): Run[] {
  const out: Run[] = [];
  for (let i = 0; i < width; i++) {
    const label = data[offset + i];
    assertValidLabel(label, labelCount);
    const last = out[out.length - 1];
    if (last && last.label === label) last.length += 1;
    else out.push({ label, length: 1 });
  }
  return out;
}

export function sliceRuns(runs: Run[], x: number, width: number): Run[] {
  const end = x + width;
  const out: Run[] = [];
  let pos = 0;
  for (const run of runs) {
    const runStart = pos;
    const runEnd = pos + run.length;
    pos = runEnd;
    const s = Math.max(runStart, x);
    const e = Math.min(runEnd, end);
    if (e > s) {
      const last = out[out.length - 1];
      if (last && last.label === run.label) last.length += e - s;
      else out.push({ label: run.label, length: e - s });
    }
    if (runEnd >= end) break;
  }
  return out;
}

export function spliceRow(runs: Run[], x: number, width: number, replacement: Run[]): Run[] {
  const end = x + width;
  const out: Run[] = [];
  let inserted = false;
  const push = (label: number, length: number): void => {
    if (length <= 0) return;
    const last = out[out.length - 1];
    if (last && last.label === label) last.length += length;
    else out.push({ label, length });
  };
  const insertReplacement = (): void => {
    for (const run of replacement) push(run.label, run.length);
    inserted = true;
  };
  let pos = 0;
  for (const run of runs) {
    const runStart = pos;
    const runEnd = pos + run.length;
    pos = runEnd;
    if (runEnd <= x) {
      push(run.label, run.length);
      continue;
    }
    if (runStart >= end) {
      if (!inserted) insertReplacement();
      push(run.label, run.length);
      continue;
    }
    if (runStart < x) push(run.label, x - runStart);
    if (!inserted) insertReplacement();
    if (runEnd > end) push(run.label, runEnd - end);
  }
  if (!inserted) insertReplacement();
  return out;
}

export function checksumRows(rows: Run[][]): number {
  let hash = 0x811c9dc5;
  const mix = (value: number): void => {
    hash ^= value >>> 0;
    hash = Math.imul(hash, 0x01000193) >>> 0;
  };
  for (const row of rows) {
    mix(row.length);
    for (const run of row) {
      mix(run.label);
      mix(run.length);
    }
  }
  return hash >>> 0;
}
