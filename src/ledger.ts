export type LedgerKind =
  | "miss"
  | "join"
  | "hit"
  | "compute-start"
  | "publish"
  | "stale-discard"
  | "cancel-drop"
  | "abort"
  | "evict"
  | "error";

export interface LedgerEntry {
  readonly seq: number;
  readonly kind: LedgerKind;
  readonly key: string;
  readonly detail: string;
}

/**
 * Append-only, sequence-numbered account of every cache decision.
 * Tests and demos can diff the ledger against an expected script.
 */
export class Ledger {
  private readonly items: LedgerEntry[] = [];
  private seq = 0;

  record(kind: LedgerKind, key: string, detail = ""): LedgerEntry {
    const entry: LedgerEntry = { seq: ++this.seq, kind, key, detail };
    this.items.push(entry);
    return entry;
  }

  entries(): readonly LedgerEntry[] {
    return this.items;
  }

  kinds(): LedgerKind[] {
    return this.items.map((entry) => entry.kind);
  }

  count(kind: LedgerKind): number {
    return this.items.filter((entry) => entry.kind === kind).length;
  }

  summary(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const entry of this.items) {
      out[entry.kind] = (out[entry.kind] ?? 0) + 1;
    }
    return out;
  }

  toString(): string {
    const header = " seq | kind          | key";
    const line = "-----+---------------+----------------------------";
    const rows = this.items.map(
      (entry) =>
        `${String(entry.seq).padStart(4)} | ${entry.kind.padEnd(13)} | ${entry.key}` +
        (entry.detail ? `  # ${entry.detail}` : ""),
    );
    return [header, line, ...rows].join("\n");
  }
}
