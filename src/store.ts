import { CdcLog } from "./cdc-log.ts";
import type { RowImage } from "./types.ts";

/**
 * Minimal in-memory table that emits CDC before/after images on every DML,
 * simulating a database binlog/CDC pipeline. Rows must contain the pk column.
 */
export class TableStore {
  readonly name: string;
  readonly pkColumn: string;
  #rows = new Map<string | number, RowImage>();
  #log: CdcLog;
  #clock: () => number;
  #txId: string | null = null;
  #txCounter = 0;

  constructor(
    name: string,
    options: { pkColumn?: string; log?: CdcLog; clock?: () => number } = {},
  ) {
    this.name = name;
    this.pkColumn = options.pkColumn ?? "id";
    this.#log = options.log ?? new CdcLog();
    this.#clock = options.clock ?? Date.now;
  }

  get log(): CdcLog {
    return this.#log;
  }

  /** Begin a transaction; all subsequent DML shares one txId until commit(). */
  beginTx(): string {
    if (this.#txId !== null) throw new Error("transaction already active");
    this.#txId = `tx-${++this.#txCounter}`;
    return this.#txId;
  }

  commitTx(): void {
    this.#txId = null;
  }

  #currentTx(): string {
    if (this.#txId === null) this.beginTx();
    return this.#txId as string;
  }

  insert(row: RowImage): void {
    const pk = row[this.pkColumn] as string | number;
    if (pk === undefined) throw new Error(`row missing pk column ${this.pkColumn}`);
    if (this.#rows.has(pk)) throw new Error(`duplicate pk ${String(pk)}`);
    const after = { ...row };
    this.#rows.set(pk, after);
    this.#log.append({
      tsMs: this.#clock(),
      txId: this.#currentTx(),
      table: this.name,
      op: "INSERT",
      pk,
      before: null,
      after,
    });
  }

  /** Update columns of one row; returns false if the row does not exist. */
  update(pk: string | number, patch: RowImage): boolean {
    const current = this.#rows.get(pk);
    if (current === undefined) return false;
    const before = { ...current };
    const after = { ...current, ...patch, [this.pkColumn]: pk };
    this.#rows.set(pk, after);
    this.#log.append({
      tsMs: this.#clock(),
      txId: this.#currentTx(),
      table: this.name,
      op: "UPDATE",
      pk,
      before,
      after,
    });
    return true;
  }

  /** Update every row matching the predicate (the classic "forgot WHERE" case). */
  updateWhere(predicate: (row: RowImage) => boolean, patch: RowImage): number {
    let count = 0;
    for (const pk of [...this.#rows.keys()]) {
      const row = this.#rows.get(pk) as RowImage;
      if (predicate(row)) {
        this.update(pk, patch);
        count++;
      }
    }
    return count;
  }

  delete(pk: string | number): boolean {
    const current = this.#rows.get(pk);
    if (current === undefined) return false;
    this.#rows.delete(pk);
    this.#log.append({
      tsMs: this.#clock(),
      txId: this.#currentTx(),
      table: this.name,
      op: "DELETE",
      pk,
      before: { ...current },
      after: null,
    });
    return true;
  }

  get(pk: string | number): RowImage | undefined {
    const row = this.#rows.get(pk);
    return row === undefined ? undefined : { ...row };
  }

  allRows(): RowImage[] {
    return [...this.#rows.values()].map((r) => ({ ...r }));
  }

  get size(): number {
    return this.#rows.size;
  }
}
