import { randomOpId, type IdGen } from "./ids.ts";
import { applyPatch, invertPatch } from "./patch.ts";
import type { Patch } from "./ops.ts";

/**
 * A text document with atomic patch application: the pre-image digest is
 * validated and the full result computed before anything is committed, so a
 * failing patch never leaves a partial state behind. Applied patches are
 * remembered together with their inverse, enabling undo.
 */
export class TextDocument {
  #text: string;
  #history: { patch: Patch; inverse: Patch }[] = [];
  #idGen: IdGen;

  constructor(text: string, idGen: IdGen = randomOpId) {
    this.#text = text;
    this.#idGen = idGen;
  }

  getText(): string {
    return this.#text;
  }

  /** Apply atomically. Throws on digest mismatch; the document is untouched. */
  apply(patch: Patch): string {
    const inverse = invertPatch(this.#text, patch, this.#idGen);
    const result = applyPatch(this.#text, patch);
    this.#text = result;
    this.#history.push({ patch, inverse });
    return result;
  }

  /** Undo the most recently applied patch. Returns false when history is empty. */
  undo(): boolean {
    const entry = this.#history.pop();
    if (!entry) return false;
    this.#text = applyPatch(this.#text, entry.inverse);
    return true;
  }
}
