/**
 * 内存账本：所有物理块分配与临时重算预留都计入同一个预算。
 * 超支时显式抛出 BudgetExceededError（明确拒绝），绝不静默超支。
 */

export class BudgetExceededError extends Error {
  readonly needed: number;
  readonly available: number;
  readonly budget: number;

  constructor(needed: number, available: number, budget: number) {
    super(`memory budget exceeded: need ${needed}B, available ${available}B, budget ${budget}B`);
    this.name = 'BudgetExceededError';
    this.needed = needed;
    this.available = available;
    this.budget = budget;
  }
}

/** 临时重算预留：在重算进行期间占用预算，结束后必须释放。 */
export class Reservation {
  readonly bytes: number;
  readonly label: string;
  private ledger: Ledger;
  private done = false;

  constructor(bytes: number, label: string, ledger: Ledger) {
    this.bytes = bytes;
    this.label = label;
    this.ledger = ledger;
  }

  get released(): boolean {
    return this.done;
  }

  release(): void {
    if (this.done) return;
    this.done = true;
    this.ledger.releaseReservation(this.bytes);
  }
}

export class Ledger {
  readonly budget: number;
  private blockBytes = 0;
  private reservedBytes = 0;

  constructor(budget: number) {
    if (!Number.isFinite(budget) || budget < 0) throw new Error(`invalid budget: ${budget}`);
    this.budget = budget;
  }

  /** 存活物理块占用的字节数。 */
  get physicalBytes(): number {
    return this.blockBytes;
  }

  /** 在途重算预留的字节数。 */
  get pendingReservedBytes(): number {
    return this.reservedBytes;
  }

  get used(): number {
    return this.blockBytes + this.reservedBytes;
  }

  get available(): number {
    return this.budget - this.used;
  }

  charge(bytes: number): void {
    if (bytes > this.available) throw new BudgetExceededError(bytes, this.available, this.budget);
    this.blockBytes += bytes;
  }

  discharge(bytes: number): void {
    this.blockBytes -= bytes;
    if (this.blockBytes < 0) throw new Error('ledger underflow: physicalBytes < 0');
  }

  reserve(bytes: number, label: string): Reservation {
    if (bytes > this.available) throw new BudgetExceededError(bytes, this.available, this.budget);
    this.reservedBytes += bytes;
    return new Reservation(bytes, label, this);
  }

  /** @internal 仅由 Reservation.release 调用。 */
  releaseReservation(bytes: number): void {
    this.reservedBytes -= bytes;
    if (this.reservedBytes < 0) throw new Error('ledger underflow: reservedBytes < 0');
  }
}
