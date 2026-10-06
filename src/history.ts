export interface HistoryEntry<T> {
  readonly id: number;
  readonly revision: number;
  readonly value: T;
}

interface MutableEntry<T> {
  id: number;
  revision: number;
  value: T;
}

export type RestoreLoader<T> = (entry: HistoryEntry<T>) => Promise<T>;

export class History<T> {
  private entries: Array<MutableEntry<T>> = [];
  private cursor = -1;
  private nextId = 1;
  private readonly capacity: number;

  constructor(capacity: number = Infinity) {
    if (!(capacity >= 1)) {
      throw new RangeError("capacity must be >= 1");
    }
    this.capacity = capacity;
  }

  get size(): number {
    return this.entries.length;
  }

  get position(): number {
    return this.cursor;
  }

  canBack(): boolean {
    return this.cursor > 0;
  }

  canForward(): boolean {
    return this.cursor >= 0 && this.cursor < this.entries.length - 1;
  }

  current(): HistoryEntry<T> | undefined {
    return this.cursor >= 0 ? this.snapshot(this.cursor) : undefined;
  }

  push(value: T): HistoryEntry<T> {
    this.entries.length = this.cursor + 1;
    const entry: MutableEntry<T> = { id: this.nextId++, revision: 1, value };
    this.entries.push(entry);
    this.cursor = this.entries.length - 1;
    this.trim();
    return { ...entry };
  }

  replace(value: T): HistoryEntry<T> {
    const current = this.entries[this.cursor];
    if (!current) {
      return this.push(value);
    }
    current.value = value;
    current.revision += 1;
    return { ...current };
  }

  back(): HistoryEntry<T> | undefined {
    if (!this.canBack()) {
      return undefined;
    }
    this.cursor -= 1;
    return this.snapshot(this.cursor);
  }

  forward(): HistoryEntry<T> | undefined {
    if (!this.canForward()) {
      return undefined;
    }
    this.cursor += 1;
    return this.snapshot(this.cursor);
  }

  restore(loader: RestoreLoader<T>): Promise<boolean> {
    const target = this.entries[this.cursor];
    if (!target) {
      return Promise.resolve(false);
    }
    const { id, revision } = target;
    return Promise.resolve()
      .then(() => loader({ ...target }))
      .then(
        (value) => this.applyRestore(id, revision, value),
        () => false,
      );
  }

  private applyRestore(id: number, revision: number, value: T): boolean {
    const index = this.entries.findIndex((entry) => entry.id === id);
    if (index < 0) {
      return false;
    }
    const entry = this.entries[index];
    if (entry.revision !== revision) {
      return false;
    }
    entry.value = value;
    entry.revision += 1;
    return true;
  }

  private trim(): void {
    const overflow = this.entries.length - this.capacity;
    if (overflow <= 0) {
      return;
    }
    this.entries.splice(0, overflow);
    this.cursor = Math.max(0, this.cursor - overflow);
  }

  private snapshot(index: number): HistoryEntry<T> {
    return { ...this.entries[index] };
  }
}
