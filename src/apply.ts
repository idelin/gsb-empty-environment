import { AppliedText, Edit, Patch, Provenance } from './model';

function sortedEdits(patch: Patch): Edit[] {
  // Array.prototype.sort is stable (ES2019+), so same-point inserts keep
  // their relative order.
  return [...patch.edits].sort((a, b) => a.start - b.start);
}

/** Throws if the patch is malformed or inconsistent with the base length. */
export function validatePatch(patch: Patch, baseLength: number): void {
  let cursor = 0;
  for (const e of sortedEdits(patch)) {
    if (!Number.isInteger(e.start) || !Number.isInteger(e.end)) {
      throw new Error(`patch ${patch.id}: edit ${e.id} has non-integer bounds`);
    }
    if (e.start < 0 || e.end < e.start || e.end > baseLength) {
      throw new Error(
        `patch ${patch.id}: edit ${e.id} [${e.start}, ${e.end}) out of bounds for base length ${baseLength}`,
      );
    }
    if (e.start === e.end && e.text === '') {
      throw new Error(`patch ${patch.id}: edit ${e.id} is a no-op empty insert`);
    }
    if (e.start < cursor) {
      throw new Error(`patch ${patch.id}: edit ${e.id} overlaps a previous edit`);
    }
    cursor = e.end;
  }
}

/**
 * Applies a patch to a base text, returning the new text together with the
 * per-character provenance (source mapping).
 *
 * Accepts either a plain string or a previous `AppliedText`; in the latter
 * case the provenance is composed, so the mapping still refers to the
 * original base text and the original edits across a chain of applies.
 */
export function applyPatch(base: string | AppliedText, patch: Patch): AppliedText {
  const text0 = typeof base === 'string' ? base : base.text;
  const prov0: Provenance[] =
    typeof base === 'string'
      ? [...text0].map((_, index) => ({ kind: 'base', index }))
      : base.provenance;
  validatePatch(patch, text0.length);
  let text = '';
  const provenance: Provenance[] = [];
  let cursor = 0;
  for (const e of sortedEdits(patch)) {
    text += text0.slice(cursor, e.start);
    for (let i = cursor; i < e.start; i++) {
      provenance.push(prov0[i]!);
    }
    text += e.text;
    for (let i = 0; i < e.text.length; i++) {
      provenance.push({ kind: 'edit', patchId: e.patchId, editId: e.id, offset: i });
    }
    cursor = e.end;
  }
  text += text0.slice(cursor);
  for (let i = cursor; i < text0.length; i++) {
    provenance.push(prov0[i]!);
  }
  return { text, provenance };
}
