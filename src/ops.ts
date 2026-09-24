/**
 * Operation model.
 *
 * A patch is a sequence of ops consumed left-to-right against the base text:
 *  - retain: copy `count` UTF-16 code units from base to output
 *  - delete: skip `count` UTF-16 code units of base; when `text` is present
 *    it must equal the deleted base content (extra context validation, and
 *    it lets the composer cancel a delete against a later re-insert)
 *  - insert: emit `text` (carries a unique operation identity `id`)
 */
export type Op =
  | { kind: "retain"; count: number }
  | { kind: "insert"; id: string; text: string }
  | { kind: "delete"; count: number; text?: string };

export interface Patch {
  /** UTF-16 length of the base text this patch applies to. */
  baseLength: number;
  /** Content digest of the base text (see digest.ts). */
  baseDigest: string;
  ops: Op[];
}

export class PatchApplyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PatchApplyError";
  }
}

export class ConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConflictError";
  }
}

/** UTF-16 code units an op consumes from its input text. */
export function inputLength(op: Op): number {
  switch (op.kind) {
    case "retain":
    case "delete":
      return op.count;
    case "insert":
      return 0;
  }
}

/** UTF-16 code units an op contributes to its output text. */
export function outputLength(op: Op): number {
  switch (op.kind) {
    case "retain":
      return op.count;
    case "delete":
      return 0;
    case "insert":
      return op.text.length;
  }
}

function deleteWithCount(count: number, text?: string): Op {
  return text === undefined
    ? { kind: "delete", count }
    : { kind: "delete", count, text };
}

function pushOp(out: Op[], op: Op): void {
  if (op.kind === "insert") {
    if (op.text.length === 0) return;
    // Inserts carry identity: never merge two inserts, even with equal text.
    out.push(op);
    return;
  }
  if (op.count === 0) return;
  const last = out[out.length - 1];
  if (last && last.kind === op.kind) {
    if (last.kind === "delete" && op.kind === "delete") {
      const merged =
        last.text !== undefined && op.text !== undefined
          ? last.text + op.text
          : undefined;
      last.count += op.count;
      if (merged === undefined) delete last.text;
      else last.text = merged;
      return;
    }
    if (last.kind === "retain" && op.kind === "retain") {
      last.count += op.count;
      return;
    }
  }
  out.push({ ...op });
}

/** Merge adjacent retains/deletes and drop empty ops. Inserts stay distinct. */
export function normalizeOps(ops: Op[]): Op[] {
  const out: Op[] = [];
  for (const op of ops) pushOp(out, op);
  return out;
}

export function makePatch(baseLength: number, baseDigest: string, ops: Op[]): Patch {
  return { baseLength, baseDigest, ops: normalizeOps(ops) };
}

/** Build a delete op, attaching the deleted content when it is known. */
export function deleteOp(count: number, text?: string): Op {
  return deleteWithCount(count, text);
}
