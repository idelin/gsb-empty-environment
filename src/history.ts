/**
 * A linear undo/redo history with stable entry ids, monotonically increasing
 * revisions, async restore bound to (id, revision), and capacity trimming.
 *
 * Semantics:
 * - push(state) discards any forward branch, appends a new entry
 *   (fresh id, revision 1) and trims the oldest entries beyond capacity.
 * - replace(state) rewrites the current entry in place: the id is kept,
 *   the revision is incremented.
 * - back()/forward() move the cursor, clamped at both ends.
 * - restore(ref, loader) resolves the loader asynchronously and applies the
 *   result only if the entry still exists and its revision still equals
 *   ref.revision; otherwise the stale result is discarded.
 */

export interface HistoryEntry<T> {
  readonly id: string;
  readonly revision: number;
  readonly state: T;
}

export interface EntryRef {
  readonly id: string;
  readonly revision: number;
}

export interface HistoryOptions {
  /** Maximum number of retained entries; oldest are trimmed. Default: Infinity. */
  capacity?: number;
  /** Id generator for pushed entries. Default: a global counter. */
  idFactory?: () => string;
}

interface MutableEntry<T> {
  id: string;
  revision: number;
  state: T;
}

let fallbackIdCounter = 0;

export class History<T> {
  _stack: Array<MutableEntry<T>> = [];
  _cursor: number = -1;
  _capacity: number;
  _nextId: () => string;

  constructor(options: HistoryOptions = {}) {
    const capacity = options.capacity === undefined ? Infinity : options.capacity;
    if (!(capacity > 0)) {
      throw new RangeError("capacity must be a positive number");
    }
    this._capacity = capacity;
    const factory = options.idFactory;
    this._nextId =
      factory === undefined
        ? () => "e" + ++fallbackIdCounter
        : factory;
  }

  get capacity(): number {
    return this._capacity;
  }

  get length(): number {
    return this._stack.length;
  }

  /** Cursor position of the current entry, -1 when empty. */
  get index(): number {
    return this._cursor;
  }

  get canBack(): boolean {
    return this._cursor > 0;
  }

  get canForward(): boolean {
    return this._cursor >= 0 && this._cursor < this._stack.length - 1;
  }

  get current(): HistoryEntry<T> | undefined {
    const entry = this._stack[this._cursor];
    return entry === undefined ? undefined : snapshot(entry);
  }

  /** Snapshot of all retained entries, oldest first. */
  entries(): Array<HistoryEntry<T>> {
    return this._stack.map(snapshot);
  }

  push(state: T): HistoryEntry<T> {
    // Discard the forward branch before appending.
    this._stack.length = this._cursor + 1;
    const entry = { id: this._nextId(), revision: 1, state: state };
    this._stack.push(entry);
    this._cursor = this._stack.length - 1;
    this._trim();
    return snapshot(entry);
  }

  replace(state: T): HistoryEntry<T> {
    const entry = this._requireCurrent();
    entry.revision += 1;
    entry.state = state;
    return snapshot(entry);
  }

  back(): HistoryEntry<T> | undefined {
    if (this._cursor > 0) {
      this._cursor -= 1;
    }
    return this.current;
  }

  forward(): HistoryEntry<T> | undefined {
    if (this._cursor < this._stack.length - 1) {
      this._cursor += 1;
    }
    return this.current;
  }

  /**
   * Asynchronously restores an entry's state via `loader`. The result is
   * applied (bumping the entry's revision) only when the entry identified
   * by `ref.id` still exists and its revision still equals `ref.revision`.
   * Resolves to true when applied, false when the result was stale.
   */
  restore(ref: EntryRef, loader: (ref: EntryRef) => Promise<T> | T): Promise<boolean> {
    const id = ref.id;
    const revision = ref.revision;
    return Promise.resolve()
      .then(() => loader({ id, revision }))
      .then((state) => this._applyRestore(id, revision, state));
  }

  _applyRestore(id: string, revision: number, state: T): boolean {
    const entry = this._stack.find((candidate) => candidate.id === id);
    if (entry === undefined || entry.revision !== revision) {
      return false;
    }
    entry.revision += 1;
    entry.state = state;
    return true;
  }

  _requireCurrent(): MutableEntry<T> {
    const entry = this._stack[this._cursor];
    if (entry === undefined) {
      throw new Error("history is empty");
    }
    return entry;
  }

  _trim(): void {
    const overflow = this._stack.length - this._capacity;
    if (overflow <= 0) {
      return;
    }
    this._stack.splice(0, overflow);
    this._cursor -= overflow;
    if (this._cursor < 0) {
      this._cursor = 0;
    }
  }
}

function snapshot<T>(entry: MutableEntry<T>): HistoryEntry<T> {
  return { id: entry.id, revision: entry.revision, state: entry.state };
}
