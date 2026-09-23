/**
 * Independent ownership ledger. The scope runtime reports every ownership
 * transition here; tests (or embedders) verify the books afterwards.
 * Invariant: every acquired resource is released exactly once — never zero,
 * never twice, never released without being acquired.
 */
export interface LedgerEntry {
  acquires: number;
  releases: number;
  events: string[];
}

export class Ledger {
  #books = new Map<string, LedgerEntry>();

  #entry(id: string): LedgerEntry {
    let entry = this.#books.get(id);
    if (!entry) {
      entry = { acquires: 0, releases: 0, events: [] };
      this.#books.set(id, entry);
    }
    return entry;
  }

  recordAcquire(id: string, note = ""): void {
    const entry = this.#entry(id);
    entry.acquires++;
    entry.events.push(note ? `acquire(${note})` : "acquire");
  }

  recordRelease(id: string, note = ""): void {
    const entry = this.#entry(id);
    entry.releases++;
    entry.events.push(note ? `release(${note})` : "release");
  }

  entry(id: string): LedgerEntry | undefined {
    const found = this.#books.get(id);
    return found ? { ...found, events: [...found.events] } : undefined;
  }

  violations(): string[] {
    const out: string[] = [];
    for (const [id, entry] of this.#books) {
      if (entry.acquires !== 1)
        out.push(`${id}: acquired ${entry.acquires}x (expected exactly 1)`);
      if (entry.releases !== 1)
        out.push(`${id}: released ${entry.releases}x (expected exactly 1)`);
    }
    return out;
  }

  assertExactOnce(): void {
    const found = this.violations();
    if (found.length > 0) {
      throw new Error(`ownership ledger violations:\n${found.join("\n")}`);
    }
  }

  snapshot(): Record<string, LedgerEntry> {
    const out: Record<string, LedgerEntry> = {};
    for (const [id, entry] of this.#books) {
      out[id] = { ...entry, events: [...entry.events] };
    }
    return out;
  }
}
