import type { Doc } from "./model.ts";
import type { MarkEdit } from "./marks.ts";
import { applyMarkEdits } from "./marks.ts";

export interface EditTransaction {
  /** Document after all edits (edits were expressed in original coordinates). */
  readonly doc: Doc;
  /**
   * Restore the document exactly as it was before the batch: same node
   * objects, same node identities, same anchors.
   */
  readonly undo: () => Doc;
}

/** Apply a batch of mark edits as one undoable transaction. */
export function applyBatch(doc: Doc, edits: readonly MarkEdit[]): EditTransaction {
  const result = applyMarkEdits(doc, edits);
  return { doc: result, undo: () => doc };
}
