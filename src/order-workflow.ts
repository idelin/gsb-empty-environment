// The local order flow as a saga: reserve inventory -> charge -> schedule delivery,
// with release / refund / cancel-shipment as compensations.
import { DeliveryAdapter, InventoryAdapter, PaymentAdapter } from './adapters.ts';
import { WorkflowExecutor } from './executor.ts';
import type { ExecutorHooks } from './executor.ts';
import { Journal } from './journal.ts';

export interface OrderInput {
  orderId: string;
  sku: string;
  quantity: number;
  account: string;
  amount: number;
  address: string;
}

export interface OrderAdapters {
  inventory: InventoryAdapter;
  payment: PaymentAdapter;
  delivery: DeliveryAdapter;
}

export function createOrderExecutor(
  order: OrderInput,
  adapters: OrderAdapters,
  journal: Journal,
  hooks: ExecutorHooks = {},
): WorkflowExecutor {
  const { inventory, payment, delivery } = adapters;
  const reserveReq = { orderId: order.orderId, sku: order.sku, quantity: order.quantity };
  const chargeReq = { orderId: order.orderId, account: order.account, amount: order.amount };
  const scheduleReq = { orderId: order.orderId, address: order.address };

  return new WorkflowExecutor(
    `order:${order.orderId}`,
    [
      {
        name: 'reserve-inventory',
        execute: (key) => inventory.reserve(key, reserveReq),
        compensate: (key) => inventory.release(key, reserveReq),
        query: async (key) => inventory.query(key),
      },
      {
        name: 'charge-payment',
        execute: (key) => payment.charge(key, chargeReq),
        compensate: (key) => payment.refund(key, chargeReq),
        query: async (key) => payment.query(key),
      },
      {
        name: 'schedule-delivery',
        execute: (key) => delivery.schedule(key, scheduleReq),
        compensate: (key) => delivery.cancel(key, scheduleReq),
        query: async (key) => delivery.query(key),
      },
    ],
    journal,
    hooks,
  );
}
