import { actionKey } from './executor.ts';
import type { FlowDef } from './executor.ts';
import {
  InventoryAdapter,
  PaymentAdapter,
  DeliveryAdapter,
} from './adapters.ts';

export interface Order {
  orderId: string;
  sku: string;
  qty: number;
  amount: number;
  address: string;
}

export interface OrderAdapters {
  inventory: InventoryAdapter;
  payment: PaymentAdapter;
  delivery: DeliveryAdapter;
}

export function makeAdapters(stockQty = 10): OrderAdapters {
  const inventory = new InventoryAdapter();
  inventory.setStock('SKU-1', stockQty);
  return { inventory, payment: new PaymentAdapter(), delivery: new DeliveryAdapter() };
}

/**
 * Local order flow: reserve inventory -> charge payment -> schedule delivery.
 * Compensations (release / refund / cancel shipment) run in reverse order and
 * only for steps confirmed successful.
 */
export function buildOrderFlow(order: Order, adapters: OrderAdapters): FlowDef {
  const { inventory, payment, delivery } = adapters;
  const chargeKey = actionKey(order.orderId, 'charge-payment');
  const scheduleKey = actionKey(order.orderId, 'schedule-delivery');
  return {
    flowId: order.orderId,
    steps: [
      {
        name: 'reserve-inventory',
        run: (key) => inventory.reserve(key, order.sku, order.qty),
        compensate: (key) => inventory.release(key, order.sku, order.qty),
        query: (key) => inventory.query(key),
      },
      {
        name: 'charge-payment',
        run: (key) => payment.charge(key, order.orderId, order.amount),
        compensate: (key) => payment.refund(key, chargeKey),
        query: (key) => payment.query(key),
      },
      {
        name: 'schedule-delivery',
        run: (key) => delivery.schedule(key, order.orderId, order.address),
        compensate: (key) => delivery.cancel(key, scheduleKey),
        query: (key) => delivery.query(key),
      },
    ],
  };
}

export interface BusinessSnapshot {
  reserved: number;
  balance: number;
  deliveryScheduled: boolean;
}

export function snapshot(order: Order, adapters: OrderAdapters): BusinessSnapshot {
  return {
    reserved: adapters.inventory.reservedOf(order.sku),
    balance: adapters.payment.balance(),
    deliveryScheduled: adapters.delivery.isScheduled(order.orderId),
  };
}

/** Business state must agree with the flow's terminal state. */
export function isConsistent(
  order: Order,
  adapters: OrderAdapters,
  terminal: string,
): boolean {
  const s = snapshot(order, adapters);
  if (terminal === 'completed') {
    return s.reserved === order.qty && s.balance === order.amount && s.deliveryScheduled;
  }
  // failed | cancelled: everything must be rolled back exactly once
  return s.reserved === 0 && s.balance === 0 && !s.deliveryScheduled;
}
