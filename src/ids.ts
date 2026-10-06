import type { NodeId } from "./model.ts";

/**
 * The public id sequence. Ids are handed out only through a reservation that
 * must be explicitly committed; a rolled-back reservation leaves the public
 * sequence untouched, so failed operations never consume ids.
 */
export class IdAllocator {
  private nextValue = 1;
  private readonly format: (n: number) => NodeId;

  constructor(format: (n: number) => NodeId = (n) => `node-${n}`) {
    this.format = format;
  }

  /** Value the next committed id will be drawn from. */
  get next(): number {
    return this.nextValue;
  }

  reserve(count: number): IdReservation {
    if (!Number.isInteger(count) || count < 1) {
      throw new RangeError(`reservation count must be a positive integer, got ${count}`);
    }
    return new IdReservation(this, count);
  }

  /** @internal */
  draw(count: number): { start: number; ids: NodeId[] } {
    const start = this.nextValue;
    const ids = Array.from({ length: count }, (_, i) => this.format(start + i));
    return { start, ids };
  }

  /** @internal */
  advanceTo(value: number): void {
    this.nextValue = value;
  }
}

export class IdReservation {
  readonly ids: NodeId[];
  private readonly start: number;
  private readonly allocator: IdAllocator;
  private settled = false;

  constructor(allocator: IdAllocator, count: number) {
    this.allocator = allocator;
    const { start, ids } = allocator.draw(count);
    this.start = start;
    this.ids = ids;
  }

  /** Publish the reserved ids: the public sequence advances past them. */
  commit(): void {
    this.ensurePending("commit");
    this.settled = true;
    this.allocator.advanceTo(this.start + this.ids.length);
  }

  /** Abandon the reservation: the public sequence stays where it was. */
  rollback(): void {
    this.ensurePending("rollback");
    this.settled = true;
  }

  private ensurePending(action: string): void {
    if (this.settled) {
      throw new Error(`reservation already settled; cannot ${action}`);
    }
  }
}
