/**
 * DOM is modeled through an adapter so the state machine can be tested without
 * a real browser.  A snapshot is deliberately not serializable: BFCache DOM
 * must only live in the browser process's memory, never in localStorage,
 * IndexedDB, a service worker cache, or a network response.
 */

export interface DomSnapshotLike {
  readonly storage: "memory";
  readonly size: number;
  readonly bytes: number;
  readonly wiped: boolean;
  read(selector: string): string | undefined;
  materialize(): Record<string, string>;
  wipe(): void;
  toJSON(): never;
}

export interface DomAdapter {
  read(selector: string): string | undefined;
  write(selector: string, value: string): void;
  snapshot(): DomSnapshotLike;
  wipe(): void;
}

export class MemoryOnlyDomSnapshot implements DomSnapshotLike {
  readonly storage = "memory" as const;
  readonly #nodes: Map<string, string>;
  #wiped = false;

  private constructor(nodes: Map<string, string>) {
    this.#nodes = nodes;
  }

  static from(nodes: ReadonlyMap<string, string>): MemoryOnlyDomSnapshot {
    return new MemoryOnlyDomSnapshot(new Map(nodes));
  }

  get size(): number {
    return this.#nodes.size;
  }

  get bytes(): number {
    let bytes = 0;
    for (const [selector, value] of this.#nodes) {
      bytes += selector.length + value.length;
    }
    return bytes;
  }

  get wiped(): boolean {
    return this.#wiped;
  }

  read(selector: string): string | undefined {
    if (this.#wiped) {
      return undefined;
    }
    return this.#nodes.get(selector);
  }

  materialize(): Record<string, string> {
    if (this.#wiped) {
      throw new Error("DOM snapshot has been wiped");
    }
    return Object.fromEntries(this.#nodes);
  }

  wipe(): void {
    for (const selector of this.#nodes.keys()) {
      this.#nodes.set(selector, "");
    }
    this.#nodes.clear();
    this.#wiped = true;
  }

  toJSON(): never {
    throw new Error(
      "BFCache DOM snapshots are memory-only and must not be serialized",
    );
  }
}

export class InMemoryDom implements DomAdapter {
  readonly #nodes = new Map<string, string>();

  constructor(initial: Readonly<Record<string, string>> = {}) {
    for (const [selector, value] of Object.entries(initial)) {
      this.#nodes.set(selector, value);
    }
  }

  read(selector: string): string | undefined {
    return this.#nodes.get(selector);
  }

  write(selector: string, value: string): void {
    this.#nodes.set(selector, value);
  }

  has(selector: string): boolean {
    return this.#nodes.has(selector);
  }

  snapshot(): MemoryOnlyDomSnapshot {
    return MemoryOnlyDomSnapshot.from(this.#nodes);
  }

  wipe(): void {
    for (const selector of this.#nodes.keys()) {
      this.#nodes.set(selector, "");
    }
    this.#nodes.clear();
  }
}
