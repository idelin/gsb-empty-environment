/**
 * Pairwise operational transformation over UTF-16 text.
 *
 * All public offsets are UTF-16 code-unit indices (i.e. plain JS string
 * indices). Positions that would split a surrogate pair are rejected.
 * Both LF and CRLF line endings are treated as ordinary characters.
 */

export type SiteId = number;

export interface InsertOp {
  kind: 'insert';
  pos: number;
  text: string;
}

export interface DeleteOp {
  kind: 'delete';
  pos: number;
  len: number;
}

export type TextOp = InsertOp | DeleteOp;

/** An operation as it travels between sites: what it does, the document
 *  version it was computed against, and which site produced it. */
export interface VersionedOp {
  op: TextOp;
  baseVersion: number;
  siteId: SiteId;
}

export function insert(pos: number, text: string): InsertOp {
  return { kind: 'insert', pos, text };
}

export function del(pos: number, len: number): DeleteOp {
  return { kind: 'delete', pos, len };
}

// ---------------------------------------------------------------------------
// Surrogate-pair safety
// ---------------------------------------------------------------------------

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** Throws if `pos` is out of range or falls between a surrogate pair. */
export function assertBoundary(doc: string, pos: number): void {
  if (!Number.isInteger(pos) || pos < 0 || pos > doc.length) {
    throw new RangeError(`offset ${pos} out of range [0, ${doc.length}]`);
  }
  if (
    pos > 0 &&
    pos < doc.length &&
    isHighSurrogate(doc.charCodeAt(pos - 1)) &&
    isLowSurrogate(doc.charCodeAt(pos))
  ) {
    throw new RangeError(`offset ${pos} splits a surrogate pair`);
  }
}

/** Throws if `text` contains any unpaired surrogate. */
export function assertWellFormed(text: string): void {
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (isHighSurrogate(code)) {
      if (i + 1 >= text.length || !isLowSurrogate(text.charCodeAt(i + 1))) {
        throw new RangeError('unpaired high surrogate in inserted text');
      }
      i++;
    } else if (isLowSurrogate(code)) {
      throw new RangeError('unpaired low surrogate in inserted text');
    }
  }
}

/** Validates `op` against `doc` and throws on any inconsistency. */
export function validateOp(doc: string, op: TextOp): void {
  if (op.kind === 'insert') {
    assertBoundary(doc, op.pos);
    assertWellFormed(op.text);
  } else {
    if (!Number.isInteger(op.len) || op.len < 0) {
      throw new RangeError(`invalid delete length ${op.len}`);
    }
    if (op.pos + op.len > doc.length) {
      throw new RangeError(
        `delete [${op.pos}, ${op.pos + op.len}) exceeds document length ${doc.length}`,
      );
    }
    assertBoundary(doc, op.pos);
    assertBoundary(doc, op.pos + op.len);
  }
}

/** Applies a single op, returning the new document. */
export function applyOp(doc: string, op: TextOp): string {
  validateOp(doc, op);
  if (op.kind === 'insert') {
    return doc.slice(0, op.pos) + op.text + doc.slice(op.pos);
  }
  return doc.slice(0, op.pos) + doc.slice(op.pos + op.len);
}

/** Applies a sequence of ops. */
export function applyOps(doc: string, ops: readonly TextOp[]): string {
  let out = doc;
  for (const op of ops) out = applyOp(out, op);
  return out;
}

// ---------------------------------------------------------------------------
// Transform
// ---------------------------------------------------------------------------

/**
 * Transforms `op` so it can be applied *after* the concurrent op `other`
 * has been applied. Returns 0–2 ops (a delete can be erased entirely by an
 * overlapping delete, or split in two by an insert/delete landing inside
 * its range).
 *
 * Insert/insert ties at the same position are broken deterministically by
 * site id: the lower site id's text ends up first, regardless of the order
 * in which the two ops are delivered.
 */
export function transform(
  op: TextOp,
  opSite: SiteId,
  other: TextOp,
  otherSite: SiteId,
): TextOp[] {
  if (op.kind === 'insert') {
    return [transformInsert(op, opSite, other, otherSite)];
  }
  return transformDelete(op, other);
}

function transformInsert(
  op: InsertOp,
  opSite: SiteId,
  other: TextOp,
  otherSite: SiteId,
): InsertOp {
  if (other.kind === 'insert') {
    if (other.pos < op.pos) {
      return insert(op.pos + other.text.length, op.text);
    }
    if (other.pos > op.pos) {
      return op;
    }
    // Same position: lower site id wins the earlier slot. Equal site ids
    // cannot produce two concurrent inserts at one position in a correct
    // session, but we stay total by not shifting.
    const otherWins = otherSite < opSite;
    return otherWins ? insert(op.pos + other.text.length, op.text) : op;
  }
  // other is a delete of [other.pos, other.pos + other.len)
  const delEnd = other.pos + other.len;
  if (op.pos <= other.pos) {
    return op;
  }
  if (op.pos >= delEnd) {
    return insert(op.pos - other.len, op.text);
  }
  // Insertion point was deleted: pin it to the start of the deleted range.
  return insert(other.pos, op.text);
}

function transformDelete(op: DeleteOp, other: TextOp): DeleteOp[] {
  const start = op.pos;
  const end = op.pos + op.len;

  if (other.kind === 'insert') {
    const insLen = other.text.length;
    if (other.pos >= end) {
      return [op];
    }
    if (other.pos <= start) {
      return [del(start + insLen, op.len)];
    }
    // Insert lands strictly inside the delete range: split. Pieces are
    // emitted right-to-left so sequential application stays in sync.
    return [
      del(other.pos + insLen, end - other.pos),
      del(start, other.pos - start),
    ];
  }

  // other is a delete of [oStart, oEnd)
  const oStart = other.pos;
  const oEnd = other.pos + other.len;

  // Pieces are emitted right-to-left so that applying them in order
  // never invalidates the coordinates of a piece yet to be applied.
  const pieces: DeleteOp[] = [];
  // Part of `op` after the other delete survives, shifted left.
  if (end > oEnd) {
    const newStart = Math.max(start, oEnd) - other.len;
    pieces.push(del(newStart, end - Math.max(start, oEnd)));
  }
  // Part of `op` before the other delete survives as-is.
  if (start < oStart) {
    pieces.push(del(start, Math.min(end, oStart) - start));
  }
  return pieces;
}

/** Transforms a list of ops (e.g. a split delete) against one other op. */
export function transformList(
  ops: readonly TextOp[],
  opSite: SiteId,
  other: TextOp,
  otherSite: SiteId,
): TextOp[] {
  const out: TextOp[] = [];
  for (const op of ops) {
    out.push(...transform(op, opSite, other, otherSite));
  }
  return out;
}
