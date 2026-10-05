/**
 * Synthetic, injectable stand-ins for the external inventory / payment /
 * delivery services. No real payment or logistics integration exists here.
 *
 * Every mutating call is keyed by a stable idempotency key. Re-invoking an
 * operation with an already-seen key returns the stored receipt WITHOUT
 * touching business state again — this is what makes both actions and
 * compensations safe to retry, and what guarantees that an already-completed
 * compensation never changes business state twice.
 *
 * Fault injection (deterministic, FIFO queue):
 *  - 'drop-receipt' : the operation IS applied to business state, but the
 *                     receipt is "lost on the wire" (ReceiptLostError). The
 *                     caller cannot tell whether it executed and must later
 *                     reconcile via query(idempotencyKey).
 *  - 'reject'       : definitive business rejection, operation NOT executed.
 *  - 'transient'    : temporary failure, operation NOT executed, retry may work.
 */

export class ReceiptLostError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ReceiptLostError';
  }
}

export class AdapterRejectedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'AdapterRejectedError';
  }
}

export class TransientAdapterError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TransientAdapterError';
  }
}

export type FaultKind = 'drop-receipt' | 'reject' | 'transient';

export interface Fault {
  kind: FaultKind;
  /** If set, the fault only fires for this operation (e.g. 'release'). */
  op?: string;
}

export abstract class SyntheticAdapter {
  readonly name: string;
  private receipts = new Map<string, unknown>();
  private faults: Fault[] = [];
  /** Idempotency keys whose business mutation was actually applied, in order. */
  readonly applyLog: string[] = [];

  constructor(name: string) {
    this.name = name;
  }

  injectFault(kind: FaultKind, op?: string): void {
    this.faults.push({ kind, op });
  }

  /** Idempotency-key lookup against the "downstream" system of record. */
  query(key: string): unknown | null {
    return this.receipts.has(key) ? (this.receipts.get(key) as unknown) : null;
  }

  /** How many times the business mutation for `key` was actually applied. */
  applyCount(key: string): number {
    return this.applyLog.filter((k) => k === key).length;
  }

  protected execute<T>(key: string, op: string, apply: () => T): T {
    const tag = `${this.name}.${op}[${key}]`;
    if (this.receipts.has(key)) {
      return this.receipts.get(key) as T; // idempotent replay: no state change
    }
    const head = this.faults[0];
    const fault = head && (head.op === undefined || head.op === op) ? this.faults.shift() : undefined;
    if (fault?.kind === 'reject') {
      throw new AdapterRejectedError(`${tag} rejected by downstream (not executed)`);
    }
    if (fault?.kind === 'transient') {
      throw new TransientAdapterError(`${tag} temporarily unavailable (not executed)`);
    }
    if (fault?.kind === 'drop-receipt') {
      const receipt = apply(); // business state changes...
      this.receipts.set(key, receipt);
      this.applyLog.push(key);
      throw new ReceiptLostError(`${tag} executed but the receipt was lost`);
    }
    const receipt = apply();
    this.receipts.set(key, receipt);
    this.applyLog.push(key);
    return receipt;
  }
}

export interface InventorySlot {
  sku: string;
  available: number;
  reserved: number;
}

export class InventoryAdapter extends SyntheticAdapter {
  readonly stock = new Map<string, InventorySlot>();

  constructor() {
    super('inventory');
  }

  setStock(sku: string, qty: number): void {
    this.stock.set(sku, { sku, available: qty, reserved: 0 });
  }

  reservedOf(sku: string): number {
    return this.stock.get(sku)?.reserved ?? 0;
  }

  reserve(key: string, sku: string, qty: number): unknown {
    return this.execute(key, 'reserve', () => {
      const slot = this.stock.get(sku);
      if (!slot || slot.available < qty) {
        throw new AdapterRejectedError(`insufficient stock for ${sku}`);
      }
      slot.available -= qty;
      slot.reserved += qty;
      return { sku, qty };
    });
  }

  release(key: string, sku: string, qty: number): unknown {
    return this.execute(key, 'release', () => {
      const slot = this.stock.get(sku);
      if (!slot) throw new AdapterRejectedError(`unknown sku ${sku}`);
      slot.available += qty;
      slot.reserved -= qty;
      return { sku, qty };
    });
  }
}

export class PaymentAdapter extends SyntheticAdapter {
  private charges = new Map<string, number>();
  private merchantBalance = 0;

  constructor() {
    super('payment');
  }

  charge(key: string, orderId: string, amount: number): unknown {
    return this.execute(key, 'charge', () => {
      if (amount <= 0) throw new AdapterRejectedError('invalid amount');
      this.charges.set(key, amount);
      this.merchantBalance += amount;
      return { orderId, amount, chargeKey: key };
    });
  }

  refund(key: string, chargeKey: string): unknown {
    return this.execute(key, 'refund', () => {
      const amount = this.charges.get(chargeKey);
      if (amount === undefined) {
        throw new AdapterRejectedError(`no such charge ${chargeKey}`);
      }
      this.charges.delete(chargeKey);
      this.merchantBalance -= amount;
      return { chargeKey, amount };
    });
  }

  balance(): number {
    return this.merchantBalance;
  }
}

export interface Shipment {
  orderId: string;
  address: string;
  status: 'scheduled' | 'cancelled';
}

export class DeliveryAdapter extends SyntheticAdapter {
  private shipments = new Map<string, Shipment>();

  constructor() {
    super('delivery');
  }

  schedule(key: string, orderId: string, address: string): unknown {
    return this.execute(key, 'schedule', () => {
      const shipment: Shipment = { orderId, address, status: 'scheduled' };
      this.shipments.set(key, shipment);
      return { orderId, scheduleKey: key };
    });
  }

  cancel(key: string, scheduleKey: string): unknown {
    return this.execute(key, 'cancel', () => {
      const shipment = this.shipments.get(scheduleKey);
      if (!shipment) throw new AdapterRejectedError(`no such shipment ${scheduleKey}`);
      shipment.status = 'cancelled';
      return { scheduleKey };
    });
  }

  isScheduled(orderId: string): boolean {
    for (const s of this.shipments.values()) {
      if (s.orderId === orderId && s.status === 'scheduled') return true;
    }
    return false;
  }
}
