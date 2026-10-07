import type { NodeId } from './graph.ts';

/**
 * Public id sequence.
 *
 * Ids are only ever handed out inside a transaction. `allocate` returns
 * provisional ids; the public counter advances solely on `commit()`.
 * A `rollback()` (or simply never committing) leaves the public sequence
 * untouched, so a failed paste can never burn ids or leave gaps.
 */
export class IdAllocator {
  private next = 0;
  private activeTransaction = false;

  /** Number of ids committed so far; the public sequence cursor. */
  get cursor(): number {
    return this.next;
  }

  beginTransaction(): IdTransaction {
    if (this.activeTransaction) {
      throw new Error('an id transaction is already active');
    }
    this.activeTransaction = true;
    let base = this.next;
    let used = 0;
    let settled = false;

    const finish = (): void => {
      settled = true;
      this.activeTransaction = false;
    };

    return {
      allocate: (): NodeId => {
        if (settled) throw new Error('id transaction is already settled');
        const id = `n_${String(base + used).padStart(6, '0')}`;
        used += 1;
        return id;
      },
      commit: (): void => {
        if (settled) throw new Error('id transaction is already settled');
        this.next = base + used;
        finish();
      },
      rollback: (): void => {
        if (settled) throw new Error('id transaction is already settled');
        finish();
      },
    };
  }
}

export interface IdTransaction {
  allocate(): NodeId;
  commit(): void;
  rollback(): void;
}
