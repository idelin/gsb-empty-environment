/**
 * Core operational transformation for a text document.
 *
 * Supported primitives: insert and delete only.
 * All positions/lengths are UTF-16 code unit offsets (same as JS string
 * indexing), so `\r\n` counts as 2 and an astral character (surrogate
 * pair) counts as 2. Operations may never split a surrogate pair.
 */

export interface InsertOp {
  kind: 'insert';
  pos: number;
  text: string;
}

export interface DeleteOp {
  kind: 'delete';
  pos: number;
  length: number;
}

export type Op = InsertOp | DeleteOp;

/** An op as exchanged between sites: tagged with its base version and author. */
export interface VersionedOp {
  baseVersion: number;
  siteId: number;
  op: Op;
}

export class InvalidBaseVersionError extends Error {
  constructor(baseVersion: number, currentVersion: number) {
    super(
      `invalid baseVersion ${baseVersion}: must be an integer in ` +
        `[0, ${currentVersion}]`,
    );
    this.name = 'InvalidBaseVersionError';
  }
}

export class InvalidOperationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidOperationError';
  }
}

export function insert(pos: number, text: string): InsertOp {
  return { kind: 'insert', pos, text };
}

export function del(pos: number, length: number): DeleteOp {
  return { kind: 'delete', pos, length };
}

const isHighSurrogate = (code: number): boolean => code >= 0xd800 && code <= 0xdbff;
const isLowSurrogate = (code: number): boolean => code >= 0xdc00 && code <= 0xdfff;

/** True if a boundary at `i` would cut a surrogate pair in two. */
export function splitsSurrogatePair(text: string, i: number): boolean {
  return (
    i > 0 &&
    i < text.length &&
    isHighSurrogate(text.charCodeAt(i - 1)) &&
    isLowSurrogate(text.charCodeAt(i))
  );
}

function assertBoundary(text: string, i: number, what: string): void {
  if (splitsSurrogatePair(text, i)) {
    throw new InvalidOperationError(
      `${what} at offset ${i} splits a surrogate pair`,
    );
  }
}

/**
 * Validate an op against the document it will be applied to.
 * Throws InvalidOperationError on out-of-range offsets, negative lengths,
 * lone surrogates in inserted text, or offsets that split a surrogate pair.
 */
export function validateOp(text: string, op: Op): void {
  if (!Number.isInteger(op.pos) || op.pos < 0 || op.pos > text.length) {
    throw new InvalidOperationError(
      `op position ${op.pos} out of range [0, ${text.length}]`,
    );
  }
  if (op.kind === 'insert') {
    if (!op.text.isWellFormed()) {
      throw new InvalidOperationError('insert text contains lone surrogates');
    }
    assertBoundary(text, op.pos, 'insert position');
  } else {
    if (!Number.isInteger(op.length) || op.length < 0) {
      throw new InvalidOperationError(`invalid delete length ${op.length}`);
    }
    if (op.pos + op.length > text.length) {
      throw new InvalidOperationError(
        `delete range [${op.pos}, ${op.pos + op.length}) exceeds document length ${text.length}`,
      );
    }
    assertBoundary(text, op.pos, 'delete start');
    assertBoundary(text, op.pos + op.length, 'delete end');
  }
}

/** Apply a single op to a document string. Assumes the op is valid. */
export function applyOp(text: string, op: Op): string {
  if (op.kind === 'insert') {
    return text.slice(0, op.pos) + op.text + text.slice(op.pos);
  }
  return text.slice(0, op.pos) + text.slice(op.pos + op.length);
}

/** Apply a sequence of ops in order. */
export function applyOps(text: string, ops: readonly Op[]): string {
  let result = text;
  for (const op of ops) result = applyOp(result, op);
  return result;
}

/**
 * Transform `op` against `against`, where `against` has already been applied
 * to the document. Returns 0..2 ops that achieve `op`'s intent on the new
 * document state (a delete can be split in two, or vanish entirely when its
 * whole range was already deleted).
 *
 * `selfSite` / `againstSite` are only used to define a stable, globally
 * consistent order for two inserts at the same position: the insert from the
 * lower site id goes first. Identical site ids fall back to comparing text,
 * which keeps the rule total and deterministic.
 */
export function transform(
  op: Op,
  against: Op,
  selfSite: number,
  againstSite: number,
): Op[] {
  if (op.kind === 'insert') {
    return [transformInsert(op, against, selfSite, againstSite)];
  }
  return transformDelete(op, against);
}

function transformInsert(
  op: InsertOp,
  against: Op,
  selfSite: number,
  againstSite: number,
): InsertOp {
  if (against.kind === 'insert') {
    let before = against.pos < op.pos;
    if (against.pos === op.pos) {
      // Stable tie-break for concurrent inserts at the same position.
      before =
        againstSite < selfSite ||
        (againstSite === selfSite && against.text <= op.text);
    }
    return before ? insert(op.pos + against.text.length, op.text) : op;
  }
  // against is a delete of [pos, pos+length).
  const { pos, length } = against;
  if (op.pos <= pos) return op;
  if (op.pos >= pos + length) return insert(op.pos - length, op.text);
  // Insert point fell inside the deleted range: clamp to the range start.
  return insert(pos, op.text);
}

/**
 * Split deletes are returned as [first, second] to be applied in order, so
 * the second segment's position is expressed in the coordinate system that
 * results from applying the first segment (i.e. shifted left by its length).
 */
function transformDelete(op: DeleteOp, against: Op): DeleteOp[] {
  const start = op.pos;
  const end = op.pos + op.length;
  if (against.kind === 'insert') {
    const q = against.pos;
    if (q >= end) return [op];
    if (q <= start) return [del(start + against.text.length, op.length)];
    // The insert landed strictly inside the delete range: split around it.
    const first = del(start, q - start);
    const second = del(q + against.text.length - first.length, end - q);
    return [first, second];
  }
  // against is a delete of [dStart, dEnd).
  const dStart = against.pos;
  const dEnd = against.pos + against.length;
  const result: DeleteOp[] = [];
  // Part of `op` before the deleted range keeps its position.
  if (start < dStart) {
    result.push(del(start, Math.min(end, dStart) - start));
  }
  // Part of `op` after the deleted range shifts left by what has already
  // been removed before it: the `against` range plus the first segment.
  if (end > dEnd) {
    const firstLen = result.length > 0 ? result[0].length : 0;
    const segStart = Math.max(start, dEnd);
    result.push(del(segStart - against.length - firstLen, end - segStart));
  }
  return result;
}

/** Transform `op` against a sequence of already-applied ops, in order. */
export function transformAgainstAll(
  op: Op,
  against: readonly { siteId: number; ops: readonly Op[] }[],
  selfSite: number,
): Op[] {
  let pending: Op[] = [op];
  for (const entry of against) {
    const next: Op[] = [];
    for (const p of pending) {
      let current: Op[] = [p];
      for (const a of entry.ops) {
        current = current.flatMap((c) => transform(c, a, selfSite, entry.siteId));
      }
      next.push(...current);
    }
    pending = next;
  }
  return pending;
}
