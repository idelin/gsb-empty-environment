// Shared fixtures for the saga tests.
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';
import { DeliveryAdapter, InventoryAdapter, PaymentAdapter } from '../src/adapters.ts';
import type { FaultEntry } from '../src/adapters.ts';
import { Journal } from '../src/journal.ts';
import type { JournalOptions } from '../src/journal.ts';
import { createOrderExecutor } from '../src/order-workflow.ts';
import type { OrderInput } from '../src/order-workflow.ts';
import type { TerminalState } from '../src/executor.ts';

export const ORDER: OrderInput = {
  orderId: 'O-1',
  sku: 'SKU-9',
  quantity: 2,
  account: 'acct-1',
  amount: 150,
  address: '1 Infinite Loop',
};

export const INITIAL_STOCK = 10;
export const INITIAL_BALANCE = 1000;

export interface World {
  dir: string;
  journalPath: string;
  inventory: InventoryAdapter;
  payment: PaymentAdapter;
  delivery: DeliveryAdapter;
  order: OrderInput;
}

export function makeWorld(faults: FaultEntry[] = []): World {
  const dir = mkdtempSync(join(tmpdir(), 'saga-'));
  return {
    dir,
    journalPath: join(dir, 'workflow.journal'),
    inventory: new InventoryAdapter({ [ORDER.sku]: INITIAL_STOCK }, faults),
    payment: new PaymentAdapter({ [ORDER.account]: INITIAL_BALANCE }, faults),
    delivery: new DeliveryAdapter(faults),
    order: { ...ORDER },
  };
}

/** Simulate a process start: fresh Journal + executor over the existing files/adapters. */
export function boot(world: World, journalOptions: JournalOptions = {}, hooks = {}) {
  const journal = Journal.open(world.journalPath, journalOptions);
  const executor = createOrderExecutor(
    world.order,
    { inventory: world.inventory, payment: world.payment, delivery: world.delivery },
    journal,
    hooks,
  );
  return { journal, executor };
}

/** Business state must match the workflow's terminal state exactly. */
export function assertConsistent(world: World, terminal: TerminalState): void {
  const { inventory, payment, delivery, order } = world;
  if (terminal === 'completed') {
    assert.equal(inventory.stock.get(order.sku), INITIAL_STOCK - order.quantity, 'stock reserved');
    assert.ok(inventory.reservations.has(order.orderId), 'reservation held');
    assert.equal(payment.balances.get(order.account), INITIAL_BALANCE - order.amount, 'amount charged');
    assert.equal(payment.charges.get(order.orderId), order.amount, 'charge recorded');
    assert.equal(delivery.shipments.get(order.orderId)?.status, 'scheduled', 'delivery scheduled');
  } else {
    // compensated | cancelled: every side effect must be undone exactly once.
    assert.equal(inventory.stock.get(order.sku), INITIAL_STOCK, 'stock fully restored');
    assert.equal(inventory.reservations.has(order.orderId), false, 'no reservation left');
    assert.equal(payment.balances.get(order.account), INITIAL_BALANCE, 'balance fully restored');
    assert.equal(payment.charges.has(order.orderId), false, 'no charge left');
    assert.notEqual(delivery.shipments.get(order.orderId)?.status, 'scheduled', 'no live shipment');
  }
}

export function terminalRecords(journal: Journal) {
  return journal.records.filter((r) => r.type === 'terminal');
}
