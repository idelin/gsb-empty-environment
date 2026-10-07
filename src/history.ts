export interface Transaction<D> {
  label: string;
  mergeKey?: string;
  before: D;
  after: D;
}

export interface CommitEntry {
  label: string;
  mergeKey?: string;
  doc: unknown;
}

export interface HistoryOptions<D> {
  capacity?: number;
  equals?: (a: D, b: D) => boolean;
  clone?: (doc: D) => D;
}

function defaultClone<D>(doc: D): D {
  return JSON.parse(JSON.stringify(doc)) as D;
}

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value) ?? "undefined";
  }
  if (Array.isArray(value)) {
    return "[" + value.map(stableStringify).join(",") + "]";
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return (
    "{" +
    keys
      .map((key) => JSON.stringify(key) + ":" + stableStringify(record[key]))
      .join(",") +
    "}"
  );
}

function defaultEquals<D>(a: D, b: D): boolean {
  return stableStringify(a) === stableStringify(b);
}

export class History<D> {
  private undoStack: Transaction<D>[] = [];
  private redoStack: Transaction<D>[] = [];
  private currentDoc: D;
  private savedSnapshot: D;
  private readonly capacity: number;
  private readonly equals: (a: D, b: D) => boolean;
  private readonly clone: (doc: D) => D;

  constructor(initial: D, options: HistoryOptions<D> = {}) {
    this.clone = options.clone ?? defaultClone;
    this.equals = options.equals ?? defaultEquals;
    this.capacity = options.capacity ?? Number.POSITIVE_INFINITY;
    this.currentDoc = this.clone(initial);
    this.savedSnapshot = this.clone(initial);
  }

  get current(): D {
    return this.clone(this.currentDoc);
  }

  get undoDepth(): number {
    return this.undoStack.length;
  }

  get redoDepth(): number {
    return this.redoStack.length;
  }

  canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  canRedo(): boolean {
    return this.redoStack.length > 0;
  }

  commit(entry: CommitEntry): void {
    const next = this.clone(entry.doc) as D;
    const top = this.undoStack[this.undoStack.length - 1];
    const adjacentToLastEdit = this.redoStack.length === 0;
    const mergeable =
      adjacentToLastEdit &&
      top !== undefined &&
      top.label === entry.label &&
      top.mergeKey === entry.mergeKey;

    if (mergeable) {
      top.after = next;
    } else {
      this.undoStack.push({
        label: entry.label,
        mergeKey: entry.mergeKey,
        before: this.currentDoc,
        after: next,
      });
      this.evictOverflow();
    }

    this.redoStack = [];
    this.currentDoc = next;
  }

  undo(): D | undefined {
    const tx = this.undoStack.pop();
    if (tx === undefined) {
      return undefined;
    }
    this.redoStack.push(tx);
    this.currentDoc = this.clone(tx.before);
    return this.current;
  }

  redo(): D | undefined {
    const tx = this.redoStack.pop();
    if (tx === undefined) {
      return undefined;
    }
    this.undoStack.push(tx);
    this.evictOverflow();
    this.currentDoc = this.clone(tx.after);
    return this.current;
  }

  markSaved(): void {
    this.savedSnapshot = this.clone(this.currentDoc);
  }

  isDirty(): boolean {
    return !this.equals(this.currentDoc, this.savedSnapshot);
  }

  private evictOverflow(): void {
    while (this.undoStack.length > this.capacity) {
      this.undoStack.shift();
    }
  }
}
