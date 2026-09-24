import { digestText } from "./digest.ts";
import { randomOpId, type IdGen } from "./ids.ts";
import {
  ConflictError,
  PatchApplyError,
  makePatch,
  normalizeOps,
  type Op,
  type Patch,
} from "./ops.ts";
import { isWellFormed, splitsSurrogatePair, utf16Length } from "./utf16.ts";

/** Validate that `base` matches the patch's recorded pre-image digest. */
export function validateBase(base: string, patch: Patch): void {
  if (utf16Length(base) !== patch.baseLength || digestText(base) !== patch.baseDigest) {
    throw new PatchApplyError(
      "base mismatch: the text does not match the patch's recorded pre-image digest",
    );
  }
}

function checkBoundary(base: string, offset: number): void {
  if (splitsSurrogatePair(base, offset)) {
    throw new PatchApplyError(
      `operation boundary at UTF-16 offset ${offset} splits a surrogate pair`,
    );
  }
}

function deleteOp(count: number, text?: string): Op {
  return text === undefined
    ? { kind: "delete", count }
    : { kind: "delete", count, text };
}

/**
 * Apply a patch to its base text.
 *
 * Atomic: the base digest is validated up front and the result is built in
 * memory; any failure throws and no partial result is produced.
 */
export function applyPatch(base: string, patch: Patch): string {
  validateBase(base, patch);
  const chunks: string[] = [];
  let pos = 0;
  for (const op of patch.ops) {
    switch (op.kind) {
      case "retain": {
        const end = pos + op.count;
        if (end > base.length) throw new PatchApplyError("retain overruns the base text");
        checkBoundary(base, pos);
        checkBoundary(base, end);
        chunks.push(base.slice(pos, end));
        pos = end;
        break;
      }
      case "delete": {
        const end = pos + op.count;
        if (end > base.length) throw new PatchApplyError("delete overruns the base text");
        checkBoundary(base, pos);
        checkBoundary(base, end);
        if (op.text !== undefined && base.slice(pos, end) !== op.text) {
          throw new PatchApplyError("delete context mismatch: base content differs");
        }
        pos = end;
        break;
      }
      case "insert": {
        if (!isWellFormed(op.text)) {
          throw new PatchApplyError("insert text contains lone surrogates");
        }
        checkBoundary(base, pos);
        chunks.push(op.text);
        break;
      }
    }
  }
  if (pos !== base.length) {
    throw new PatchApplyError("patch does not consume the whole base text");
  }
  return chunks.join("");
}

/**
 * Invert a patch against its base text. The inverse re-inserts deleted
 * content with fresh operation identities, so a reverted edit is still a
 * distinct edit and cannot be confused with the original insertion.
 */
export function invertPatch(base: string, patch: Patch, idGen: IdGen = randomOpId): Patch {
  validateBase(base, patch);
  const ops: Op[] = [];
  let pos = 0;
  for (const op of patch.ops) {
    switch (op.kind) {
      case "retain":
        ops.push({ kind: "retain", count: op.count });
        pos += op.count;
        break;
      case "delete": {
        const end = pos + op.count;
        if (end > base.length) throw new PatchApplyError("delete overruns the base text");
        checkBoundary(base, pos);
        checkBoundary(base, end);
        const deleted = base.slice(pos, end);
        if (op.text !== undefined && op.text !== deleted) {
          throw new PatchApplyError("delete context mismatch: base content differs");
        }
        ops.push({ kind: "insert", id: idGen(), text: deleted });
        pos = end;
        break;
      }
      case "insert":
        ops.push(deleteOp(op.text.length, op.text));
        break;
    }
  }
  if (pos !== base.length) {
    throw new PatchApplyError("patch does not consume the whole base text");
  }
  const result = applyPatch(base, patch);
  return makePatch(utf16Length(result), digestText(result), ops);
}

function commonPrefixLen(a: string, b: string): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a.charCodeAt(i) === b.charCodeAt(i)) i += 1;
  return i;
}

/**
 * Compose two patches so that apply(base, compose(a, b)) ===
 * apply(apply(base, a), b).
 *
 * Adjacent inverse operations are eliminated during the streaming walk:
 *  - when `b` deletes characters that `a` just inserted, insert and delete
 *    cancel (fully or partially);
 *  - when `a` deletes content that `b` re-inserts verbatim (e.g. b is a's
 *    inverse), the pair collapses to a retain — possible because deletes
 *    carry their content as context.
 * Because cancellation happens in-stream, the coordinates later operations
 * refer to stay anchored to the same origin and the composed patch remains
 * positionally exact.
 */
export function composePatches(a: Patch, b: Patch): Patch {
  const ops: Op[] = [];
  let i = 0;
  let j = 0;
  let opA: Op | undefined = a.ops[0];
  let opB: Op | undefined = b.ops[0];
  const advanceA = (): void => {
    opA = a.ops[++i];
  };
  const advanceB = (): void => {
    opB = b.ops[++j];
  };

  while (opA !== undefined || opB !== undefined) {
    if (opA === undefined) {
      if (opB === undefined) break;
      if (opB.kind === "insert") {
        ops.push(opB);
        advanceB();
      } else {
        throw new PatchApplyError("compose: patch b consumes more input than a produces");
      }
      continue;
    }
    if (opB === undefined) {
      if (opA.kind === "retain") {
        throw new PatchApplyError("compose: patch b retains less than a produces");
      }
      ops.push(opA);
      advanceA();
      continue;
    }

    if (opA.kind === "delete" && opB.kind === "insert" && opA.text !== undefined) {
      // Inverse-op elimination: b re-inserts (a prefix of) what a deleted.
      const k = commonPrefixLen(opA.text, opB.text);
      if (k > 0) ops.push({ kind: "retain", count: k });
      if (k < opA.text.length) {
        if (k === 0) {
          // No shared content: an ordinary delete followed by an insert.
          ops.push(opA);
          advanceA();
        } else {
          opA = deleteOp(opA.text.length - k, opA.text.slice(k));
        }
      } else {
        advanceA();
      }
      if (k > 0) {
        if (k < opB.text.length) {
          opB = { kind: "insert", id: opB.id, text: opB.text.slice(k) };
        } else {
          advanceB();
        }
      }
      continue;
    }
    if (opA.kind === "delete") {
      // a deletes base characters; invisible to b.
      ops.push(opA);
      advanceA();
      continue;
    }
    if (opB.kind === "insert") {
      // b inserts text that does not come from a's output.
      ops.push(opB);
      advanceB();
      continue;
    }
    if (opA.kind === "insert" && opB.kind === "delete") {
      // Inverse-op elimination: b deletes (part of) what a inserted.
      const n = Math.min(opA.text.length, opB.count);
      const restText = opA.text.slice(n);
      if (restText.length === 0) {
        advanceA();
      } else {
        opA = { kind: "insert", id: opA.id, text: restText };
      }
      if (n === opB.count) {
        advanceB();
      } else {
        opB = deleteOp(opB.count - n, opB.text?.slice(n));
      }
      continue;
    }
    if (opA.kind === "insert") {
      // b retains (part of) a's inserted text: the insert passes through and
      // b's retain is consumed by the same amount, keeping both streams
      // aligned for the operations that follow.
      const n = Math.min(opA.text.length, opB.count);
      ops.push({ kind: "insert", id: opA.id, text: opA.text.slice(0, n) });
      if (n === opA.text.length) {
        advanceA();
      } else {
        opA = { kind: "insert", id: opA.id, text: opA.text.slice(n) };
      }
      if (n === opB.count) {
        advanceB();
      } else {
        opB = { kind: "retain", count: opB.count - n };
      }
      continue;
    }
    if (opB.kind === "delete") {
      // b deletes characters a retained: composed as a delete of the base.
      const n = Math.min(opA.count, opB.count);
      ops.push(deleteOp(n, opB.text?.slice(0, n)));
      if (n === opA.count) {
        advanceA();
      } else {
        opA = { kind: "retain", count: opA.count - n };
      }
      if (n === opB.count) {
        advanceB();
      } else {
        opB = deleteOp(opB.count - n, opB.text?.slice(n));
      }
      continue;
    }
    // retain x retain
    const n = Math.min(opA.count, opB.count);
    ops.push({ kind: "retain", count: n });
    if (n === opA.count) {
      advanceA();
    } else {
      opA = { kind: "retain", count: opA.count - n };
    }
    if (n === opB.count) {
      advanceB();
    } else {
      opB = { kind: "retain", count: opB.count - n };
    }
  }

  return makePatch(a.baseLength, a.baseDigest, ops);
}

export type Side = "left" | "right";

/**
 * Transform `a`'s ops against concurrent `b`'s ops (both based on the same
 * text), producing ops for a' that apply on top of b's result. Insert
 * operation ids give a deterministic order when both sides insert at the
 * same position; `side` is the final tie-break.
 *
 * Overlapping (crossing) deletions are a conflict and throw ConflictError.
 */
export function transformOps(a: Op[], b: Op[], side: Side = "left"): Op[] {
  const ops: Op[] = [];
  let i = 0;
  let j = 0;
  let opA: Op | undefined = a[0];
  let opB: Op | undefined = b[0];
  const advanceA = (): void => {
    opA = a[++i];
  };
  const advanceB = (): void => {
    opB = b[++j];
  };

  while (opA !== undefined || opB !== undefined) {
    if (opA === undefined) {
      // Remaining b ops: a' must retain over b's inserts; b's deletes remove
      // text a did not touch, so nothing is emitted for them.
      if (opB === undefined) break;
      if (opB.kind === "insert") ops.push({ kind: "retain", count: opB.text.length });
      advanceB();
      continue;
    }
    if (opB === undefined) {
      ops.push(opA);
      advanceA();
      continue;
    }

    if (opA.kind === "insert" && opB.kind === "insert") {
      // Deterministic order: lower op id first, side as final tie-break.
      const aFirst = opA.id === opB.id ? side === "left" : opA.id < opB.id;
      if (aFirst) {
        ops.push(opA);
        advanceA();
      } else {
        ops.push({ kind: "retain", count: opB.text.length });
        advanceB();
      }
      continue;
    }
    if (opA.kind === "insert") {
      ops.push(opA);
      advanceA();
      continue;
    }
    if (opB.kind === "insert") {
      ops.push({ kind: "retain", count: opB.text.length });
      advanceB();
      continue;
    }
    if (opA.kind === "delete" && opB.kind === "delete") {
      throw new ConflictError("crossing deletions: both patches delete overlapping ranges");
    }
    if (opA.kind === "delete" && opB.kind === "retain") {
      const n = Math.min(opA.count, opB.count);
      ops.push(deleteOp(n, opA.text?.slice(0, n)));
      if (n === opA.count) advanceA();
      else opA = deleteOp(opA.count - n, opA.text?.slice(n));
      if (n === opB.count) advanceB();
      else opB = { kind: "retain", count: opB.count - n };
      continue;
    }
    if (opA.kind === "retain" && opB.kind === "delete") {
      // b already removed these characters; a' has nothing to do for them.
      const n = Math.min(opA.count, opB.count);
      if (n === opA.count) advanceA();
      else opA = { kind: "retain", count: opA.count - n };
      if (n === opB.count) advanceB();
      else opB = deleteOp(opB.count - n, opB.text?.slice(n));
      continue;
    }
    // retain x retain
    const n = Math.min(
      (opA as { count: number }).count,
      (opB as { count: number }).count,
    );
    ops.push({ kind: "retain", count: n });
    if (n === (opA as { count: number }).count) advanceA();
    else opA = { kind: "retain", count: (opA as { count: number }).count - n };
    if (n === (opB as { count: number }).count) advanceB();
    else opB = { kind: "retain", count: (opB as { count: number }).count - n };
  }

  return normalizeOps(ops);
}

/**
 * Three-way relocation: rebase `a` (defined on `base`) onto the result of
 * applying the concurrent patch `b`. Returns a' whose recorded pre-image is
 * apply(base, b), so apply(apply(base, b), a') is the relocated result.
 */
export function rebasePatch(base: string, a: Patch, b: Patch, side: Side = "left"): Patch {
  validateBase(base, a);
  validateBase(base, b);
  const afterB = applyPatch(base, b);
  const ops = transformOps(a.ops, b.ops, side);
  return makePatch(utf16Length(afterB), digestText(afterB), ops);
}
