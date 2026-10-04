// Synthetic, fully in-memory stand-ins for inventory / payment / logistics.
// They model a *durable* external system: tests keep one adapter instance
// alive while the executor around it crashes and restarts.
//
// Every mutating call takes a stable idempotency key. Replaying a call with a
// known key returns the stored receipt without touching business state, so
// retries after lost receipts are safe. query(key) is the reconciliation
// endpoint the executor uses on recovery.
//
// Fault injection is a deterministic script: [{method, occurrence, fault}].
// occurrence counts calls to that method (1-based), which makes fault runs
// exactly replayable.
import { AdapterBusinessError, ReceiptLostError, TransientAdapterError } from './errors.ts';

export type FaultKind = 'error' | 'transient' | 'lost-receipt';

export interface FaultEntry {
  /** e.g. 'inventory.reserve', 'payment.refund' */
  method: string;
  /** 1-based call count of that method at which the fault fires */
  occurrence: number;
  fault: FaultKind;
}

export type Receipt =
  | { kind: 'execute'; result: unknown }
  | { kind: 'compensate'; result: unknown };

export abstract class SyntheticAdapter {
  /** Durable receipts of everything this system has committed, by idempotency key. */
  protected receipts = new Map<string, Receipt>();
  private callCounts = new Map<string, number>();

  readonly name: string;
  private faultScript: FaultEntry[];

  constructor(name: string, faultScript: FaultEntry[] = []) {
    this.name = name;
    this.faultScript = faultScript;
  }

  setFaultScript(script: FaultEntry[]): void {
    this.faultScript = script;
  }

  callCount(method: string): number {
    return this.callCounts.get(method) ?? 0;
  }

  /** Reconciliation view used by the executor after a crash or lost receipt. */
  query(idempotencyKey: string): Receipt | null {
    return this.receipts.get(idempotencyKey) ?? null;
  }

  /**
   * Run one idempotent operation. `apply` performs the actual business-state
   * mutation and is executed at most once per idempotency key.
   */
  protected invoke<T>(method: string, key: string, kind: 'execute' | 'compensate', apply: () => T): T {
    const fullMethod = `${this.name}.${method}`;
    const count = (this.callCounts.get(fullMethod) ?? 0) + 1;
    this.callCounts.set(fullMethod, count);

    const known = this.receipts.get(key);
    if (known) return known.result as T; // idempotent replay: no state change

    const fault = this.faultScript.find((f) => f.method === fullMethod && f.occurrence === count);
    if (fault?.fault === 'error') {
      throw new AdapterBusinessError(`${fullMethod} rejected the request (scripted fault)`);
    }
    if (fault?.fault === 'transient') {
      throw new TransientAdapterError(`${fullMethod} temporarily unavailable (scripted fault)`);
    }

    const result = apply();
    this.receipts.set(key, { kind, result });

    if (fault?.fault === 'lost-receipt') {
      // State was committed above; only the response back to the caller is lost.
      throw new ReceiptLostError(key);
    }
    return result;
  }
}

// ---------------------------------------------------------------- inventory

export interface ReserveRequest {
  orderId: string;
  sku: string;
  quantity: number;
}

export class InventoryAdapter extends SyntheticAdapter {
  readonly stock = new Map<string, number>();
  readonly reservations = new Map<string, { sku: string; quantity: number }>();

  constructor(initialStock: Record<string, number>, faultScript: FaultEntry[] = []) {
    super('inventory', faultScript);
    for (const [sku, qty] of Object.entries(initialStock)) this.stock.set(sku, qty);
  }

  async reserve(key: string, req: ReserveRequest): Promise<{ reservationId: string }> {
    return this.invoke('reserve', key, 'execute', () => {
      const available = this.stock.get(req.sku) ?? 0;
      if (available < req.quantity) {
        throw new AdapterBusinessError(`insufficient stock for ${req.sku}`);
      }
      this.stock.set(req.sku, available - req.quantity);
      this.reservations.set(req.orderId, { sku: req.sku, quantity: req.quantity });
      return { reservationId: `rsv:${req.orderId}` };
    });
  }

  async release(key: string, req: ReserveRequest): Promise<{ released: true }> {
    return this.invoke('release', key, 'compensate', () => {
      const held = this.reservations.get(req.orderId);
      if (held) {
        this.reservations.delete(req.orderId);
        this.stock.set(held.sku, (this.stock.get(held.sku) ?? 0) + held.quantity);
      }
      return { released: true };
    });
  }
}

// ------------------------------------------------------------------ payment

export interface ChargeRequest {
  orderId: string;
  account: string;
  amount: number;
}

export class PaymentAdapter extends SyntheticAdapter {
  readonly balances = new Map<string, number>();
  readonly charges = new Map<string, number>();

  constructor(initialBalances: Record<string, number>, faultScript: FaultEntry[] = []) {
    super('payment', faultScript);
    for (const [account, amount] of Object.entries(initialBalances)) this.balances.set(account, amount);
  }

  async charge(key: string, req: ChargeRequest): Promise<{ transactionId: string }> {
    return this.invoke('charge', key, 'execute', () => {
      const balance = this.balances.get(req.account) ?? 0;
      if (balance < req.amount) {
        throw new AdapterBusinessError(`insufficient funds in ${req.account}`);
      }
      this.balances.set(req.account, balance - req.amount);
      this.charges.set(req.orderId, req.amount);
      return { transactionId: `txn:${req.orderId}` };
    });
  }

  async refund(key: string, req: ChargeRequest): Promise<{ refunded: true }> {
    return this.invoke('refund', key, 'compensate', () => {
      const charged = this.charges.get(req.orderId);
      if (charged !== undefined) {
        this.charges.delete(req.orderId);
        this.balances.set(req.account, (this.balances.get(req.account) ?? 0) + charged);
      }
      return { refunded: true };
    });
  }
}

// ----------------------------------------------------------------- delivery

export interface ScheduleRequest {
  orderId: string;
  address: string;
}

export class DeliveryAdapter extends SyntheticAdapter {
  readonly shipments = new Map<string, { address: string; status: 'scheduled' | 'cancelled' }>();

  constructor(faultScript: FaultEntry[] = []) {
    super('delivery', faultScript);
  }

  async schedule(key: string, req: ScheduleRequest): Promise<{ shipmentId: string }> {
    return this.invoke('schedule', key, 'execute', () => {
      this.shipments.set(req.orderId, { address: req.address, status: 'scheduled' });
      return { shipmentId: `shp:${req.orderId}` };
    });
  }

  async cancel(key: string, req: ScheduleRequest): Promise<{ cancelled: true }> {
    return this.invoke('cancel', key, 'compensate', () => {
      const shipment = this.shipments.get(req.orderId);
      if (shipment) shipment.status = 'cancelled';
      return { cancelled: true };
    });
  }
}
