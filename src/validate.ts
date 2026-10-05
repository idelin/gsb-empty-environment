/**
 * Independent validator. Re-reads files from disk, re-decodes fragments with
 * its own sequential pass, and cross-checks:
 *   - directory coverage: ordered, non-overlapping half-open ranges
 *   - per-shard count and sha256 digest against actual fragment bytes
 *   - ids strictly increasing and contained in their shard range
 *   - cursor integrity and binding to expression hash and input versions
 *   - paged query results against a full in-memory set-operation reference
 */
import * as fs from 'fs';
import { Expr } from './expr';
import { CursorPayload, assertCursorBinding, decodeCursor } from './cursor';
import {
  FormatError,
  SetFileHeader,
  decodeFragment,
  parseDirectory,
  parseHeader,
  sha256Hex,
} from './format';

export interface FileValidation {
  ok: boolean;
  errors: string[];
  setId?: string;
  version?: string;
  shardCount?: number;
  totalIds?: number;
}

export function validateFile(path: string): FileValidation {
  const errors: string[] = [];
  let buf: Buffer;
  try {
    buf = fs.readFileSync(path);
  } catch (e) {
    return { ok: false, errors: ['cannot read file: ' + (e as Error).message] };
  }
  let header: SetFileHeader;
  try {
    const head = parseHeader(buf);
    const shards = parseDirectory(buf, head.shardCount);
    header = { setId: head.setId, version: head.version, shards };
  } catch (e) {
    return { ok: false, errors: ['header/directory: ' + (e as Error).message] };
  }

  const shards = header.shards;
  let totalIds = 0;
  for (let i = 0; i < shards.length; i++) {
    const s = shards[i];
    const where = 'shard ' + i;
    if (s.rangeStart > s.rangeEnd) {
      errors.push(where + ': rangeStart ' + s.rangeStart + ' > rangeEnd ' + s.rangeEnd);
    }
    if (i > 0 && s.rangeStart < shards[i - 1].rangeEnd) {
      errors.push(
        where + ': range [' + s.rangeStart + ',' + s.rangeEnd + ') overlaps previous shard range [' +
        shards[i - 1].rangeStart + ',' + shards[i - 1].rangeEnd + ')',
      );
    }
    if (s.fragOffset + s.fragLength > buf.length) {
      errors.push(where + ': fragment extends past end of file');
      continue;
    }
    const frag = buf.slice(s.fragOffset, s.fragOffset + s.fragLength);
    const digest = sha256Hex(frag);
    if (digest !== s.digest) {
      errors.push(where + ': digest mismatch (directory ' + s.digest.slice(0, 16) + '... != actual ' + digest.slice(0, 16) + '...)');
      continue;
    }
    const ids: number[] = [];
    try {
      const blocks = decodeFragment(frag);
      for (const b of blocks) ids.push(...b.ids);
    } catch (e) {
      errors.push(where + ': fragment decode failed: ' + (e as Error).message);
      continue;
    }
    if (ids.length !== s.count) {
      errors.push(where + ': count mismatch (directory says ' + s.count + ', fragment holds ' + ids.length + ')');
    }
    for (let j = 0; j < ids.length; j++) {
      if (j > 0 && ids[j] <= ids[j - 1]) {
        errors.push(where + ': ids not strictly increasing at position ' + j);
        break;
      }
      if (ids[j] < s.rangeStart || ids[j] >= s.rangeEnd) {
        errors.push(where + ': id ' + ids[j] + ' outside shard range [' + s.rangeStart + ',' + s.rangeEnd + ')');
        break;
      }
    }
    totalIds += ids.length;
  }
  return {
    ok: errors.length === 0,
    errors,
    setId: header.setId,
    version: header.version,
    shardCount: shards.length,
    totalIds,
  };
}

/** Fully decode a set file into one sorted array (reference path, no cache). */
export function loadSetIds(path: string): number[] {
  const buf = fs.readFileSync(path);
  const head = parseHeader(buf);
  const shards = parseDirectory(buf, head.shardCount);
  const out: number[] = [];
  for (let i = 0; i < shards.length; i++) {
    const s = shards[i];
    const frag = buf.slice(s.fragOffset, s.fragOffset + s.fragLength);
    if (sha256Hex(frag) !== s.digest) {
      throw new FormatError('shard ' + i + ': digest mismatch while loading reference ids');
    }
    for (const b of decodeFragment(frag)) out.push(...b.ids);
  }
  return out;
}

function intersect(a: number[], b: number[]): number[] {
  const out: number[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push(a[i]);
      i++;
      j++;
    } else if (a[i] < b[j]) i++;
    else j++;
  }
  return out;
}

function union(a: number[], b: number[]): number[] {
  const out: number[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (j >= b.length || (i < a.length && a[i] < b[j])) out.push(a[i++]);
    else if (i >= a.length || b[j] < a[i]) out.push(b[j++]);
    else {
      out.push(a[i]);
      i++;
      j++;
    }
  }
  return out;
}

/** Reference evaluation: full in-memory set algebra over completely expanded sets. */
export function referenceEval(expr: Expr, sets: Map<string, number[]>): number[] {
  if (expr.kind === 'set') {
    const ids = sets.get(expr.set);
    if (!ids) throw new Error('reference: unknown set "' + expr.set + '"');
    return ids;
  }
  const parts = expr.children.map((c) => referenceEval(c, sets));
  if (parts.length === 0) return [];
  return parts.reduce((acc, cur) => (expr.kind === 'and' ? intersect(acc, cur) : union(acc, cur)));
}

export interface CursorValidation {
  ok: boolean;
  errors: string[];
  payload?: CursorPayload;
}

/** Check cursor integrity and binding against the expected expression hash and inputs. */
export function validateCursorBinding(
  cursor: string,
  exprHash: string,
  inputs: [string, string][],
): CursorValidation {
  const errors: string[] = [];
  let payload: CursorPayload | undefined;
  try {
    payload = decodeCursor(cursor);
  } catch (e) {
    return { ok: false, errors: ['cursor decode: ' + (e as Error).message] };
  }
  try {
    assertCursorBinding(payload, exprHash, inputs);
  } catch (e) {
    errors.push('cursor binding: ' + (e as Error).message);
  }
  return { ok: errors.length === 0, errors, payload };
}

/** Compare concatenated pages against the reference result (catches misses and duplicates). */
export function validatePages(pages: number[][], reference: number[]): string[] {
  const errors: string[] = [];
  const got: number[] = [];
  pages.forEach((p, i) => {
    if (p.length === 0 && (pages.length > 1 || reference.length > 0)) {
      errors.push('page ' + i + ' is empty but more results were expected');
    }
    got.push(...p);
  });
  for (let i = 1; i < got.length; i++) {
    if (got[i] <= got[i - 1]) {
      errors.push('result not strictly increasing at global position ' + i + ': ' + got[i - 1] + ' then ' + got[i]);
      break;
    }
  }
  if (got.length !== reference.length) {
    errors.push('result length ' + got.length + ' != reference length ' + reference.length);
  } else {
    for (let i = 0; i < got.length; i++) {
      if (got[i] !== reference[i]) {
        errors.push('result differs from reference at position ' + i + ': got ' + got[i] + ', want ' + reference[i]);
        break;
      }
    }
  }
  return errors;
}
