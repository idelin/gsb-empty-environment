import type { Transaction, TransactionalEditor } from "./editor.ts";
import {
  DEFAULT_COMPLETION_AFFINITY,
  type Range,
  type RangeAffinity,
  type TextEdit,
} from "./types.ts";

/** One completion as returned by the language server, offsets based at `CompletionResponse.version`. */
export interface CompletionItem {
  readonly label: string;
  /** Text that replaces `range` when the item is accepted. */
  readonly replacement: string;
  /** Replacement range, valid at the response's document version. */
  readonly range: Range;
}

/** A completion response computed against a specific (possibly old) document version. */
export interface CompletionResponse {
  readonly version: number;
  readonly items: readonly CompletionItem[];
}

/** A completion item with its range mapped to the current document version. */
export interface MappedCompletionItem {
  readonly item: CompletionItem;
  readonly range: Range;
}

/** Thrown when a completion can no longer be reconciled with the document. */
export class StaleCompletionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "StaleCompletionError";
  }
}

/**
 * Tracks one completion response while the user keeps editing.
 *
 * Ranges in the response are anchored at `response.version`; every lookup
 * maps them through the version edit chain to the current version. If an
 * edit touched the interior of a range, that range — and therefore the
 * response — is stale. Accepting an item applies it (plus any secondary
 * cursor ranges) as a single undoable transaction.
 */
export class CompletionSession {
  private readonly editor: TransactionalEditor;
  private readonly response: CompletionResponse;
  private readonly affinity: RangeAffinity;

  constructor(
    editor: TransactionalEditor,
    response: CompletionResponse,
    affinity: RangeAffinity = DEFAULT_COMPLETION_AFFINITY,
  ) {
    this.editor = editor;
    this.response = response;
    this.affinity = affinity;
    const chain = editor.document.chain;
    if (!chain.hasVersion(response.version)) {
      throw new StaleCompletionError(
        `response version ${response.version} is outside the tracked window ` +
          `[${chain.baseVersion}, ${chain.currentVersion}]`,
      );
    }
  }

  /** Document version the response was computed against. */
  get responseVersion(): number {
    return this.response.version;
  }

  /** Current document version. */
  get currentVersion(): number {
    return this.editor.version;
  }

  /** True while every item's range still maps cleanly to the current version. */
  isValid(): boolean {
    const chain = this.editor.document.chain;
    return this.response.items.every(
      (item) => chain.mapRange(item.range, this.response.version, this.affinity).valid,
    );
  }

  /** All items with ranges mapped to the current version. Throws if the response is stale. */
  items(): MappedCompletionItem[] {
    return this.response.items.map((item) => ({ item, range: this.mapRange(item.range) }));
  }

  /**
   * Accept an item from the response.
   *
   * `cursors` holds the replacement ranges of any secondary cursors,
   * expressed at the response version; each is mapped through the edit
   * chain and replaced with the same text. All replacements are applied
   * as one undoable transaction. If any range is stale, nothing is
   * applied and a StaleCompletionError is thrown.
   */
  accept(item: CompletionItem, cursors: readonly Range[] = []): Transaction {
    if (!this.response.items.includes(item)) {
      throw new Error("item does not belong to this session's response");
    }
    const ranges = [item.range, ...cursors].map((r) => this.mapRange(r));
    const edits: TextEdit[] = ranges.map((r) => ({ start: r.start, end: r.end, text: item.replacement }));
    try {
      return this.editor.transact(edits);
    } catch (error) {
      if (error instanceof Error && error.message.includes("overlap")) {
        throw new StaleCompletionError("mapped cursor ranges overlap");
      }
      throw error;
    }
  }

  private mapRange(range: Range): Range {
    const result = this.editor.document.chain.mapRange(range, this.response.version, this.affinity);
    if (!result.valid) {
      throw new StaleCompletionError(
        `range [${range.start}, ${range.end}) from version ${this.response.version} ` +
          `was invalidated by a later edit`,
      );
    }
    return result.range;
  }
}
