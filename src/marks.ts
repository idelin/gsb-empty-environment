import type { Doc, Mark } from "./model.ts";
import { docLength, isLinkMark, validateDoc } from "./model.ts";
import type { FlatCell } from "./flat.ts";
import { flatten, fromFlat } from "./flat.ts";
import { normalize } from "./normalize.ts";

export interface MarkEdit {
  readonly kind: "add" | "remove";
  /** Character range in the *original* document, half-open [start, end). */
  readonly start: number;
  readonly end: number;
  readonly mark: Mark;
}

export class LinkNestingError extends Error {
  constructor(mark: Mark, start: number, end: number) {
    super(`cannot apply link mark "${mark}" over [${start}, ${end}): range already contains a link`);
    this.name = "LinkNestingError";
  }
}

function checkRange(doc: Doc, start: number, end: number): void {
  const len = docLength(doc);
  if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start || end > len) {
    throw new RangeError(`invalid range [${start}, ${end}) for document of length ${len}`);
  }
}

/**
 * Apply a batch of mark edits expressed in original-document coordinates.
 * Nodes are split at edit boundaries, marks are added/removed per character,
 * and the result is regrouped into maximal same-format runs (split-then-merge).
 * Anchors stay pinned to their character boundary; when a split lands exactly
 * on an anchor, its affinity decides which side owns it.
 *
 * Node identity: the first surviving piece of an original node keeps its id;
 * later pieces get derived ids (`<id>~<n>`).
 */
export function applyMarkEdits(doc: Doc, edits: readonly MarkEdit[]): Doc {
  validateDoc(doc);
  for (const e of edits) checkRange(doc, e.start, e.end);

  const flat = flatten(doc);
  const cells: FlatCell[] = flat.cells.map((c) => ({ char: c.char, marks: [...c.marks] }));

  for (const edit of edits) {
    if (edit.start === edit.end) continue;
    if (edit.kind === "add" && isLinkMark(edit.mark)) {
      for (let i = edit.start; i < edit.end; i++) {
        if (cells[i].marks.some(isLinkMark)) {
          throw new LinkNestingError(edit.mark, edit.start, edit.end);
        }
      }
    }
    for (let i = edit.start; i < edit.end; i++) {
      const set = new Set(cells[i].marks);
      if (edit.kind === "add") set.add(edit.mark);
      else set.delete(edit.mark);
      cells[i] = { char: cells[i].char, marks: [...set].sort() };
    }
  }

  const source = doc.nodes;
  const rebuilt = fromFlat(
    { cells, boundaries: flat.boundaries, owners: flat.owners },
    (owner, seq) =>
      owner === null ? `n~${seq}` : seq === 0 ? source[owner].id : `${source[owner].id}~${seq}`,
  );
  // Split-then-merge: regroup into maximal same-format runs.
  return normalize(rebuilt).doc;
}

export function addMark(doc: Doc, start: number, end: number, mark: Mark): Doc {
  return applyMarkEdits(doc, [{ kind: "add", start, end, mark }]);
}

export function removeMark(doc: Doc, start: number, end: number, mark: Mark): Doc {
  return applyMarkEdits(doc, [{ kind: "remove", start, end, mark }]);
}
