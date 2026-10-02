/** Shared fixture: a 3-level nested order-book schema used by tests and the demo. */
import type { Schema } from '../../src/schema.ts';

export const UserSchema: Schema = {
  name: 'User',
  fields: [
    { name: 'user_id', number: 1, type: 'varint', key: true },
    { name: 'name', number: 2, type: 'string' },
    { name: 'tier', number: 3, type: 'zigzag' },
  ],
};

export const OrderSchema: Schema = {
  name: 'Order',
  fields: [
    { name: 'order_id', number: 1, type: 'varint', key: true },
    { name: 'symbol', number: 2, type: 'string' },
    { name: 'price', number: 3, type: 'fixed64' },
    { name: 'qty', number: 4, type: 'zigzag' },
    { name: 'user_id', number: 5, type: 'varint', ref: { entity: 'User' } },
    { name: 'fills', number: 6, type: 'varint', repeated: true, packed: true },
    { name: 'next', number: 7, type: 'varint', ref: { entity: 'Order' } },
  ],
};

export const OrderBookSchema: Schema = {
  name: 'OrderBook',
  fields: [
    { name: 'users', number: 1, type: 'varint', message: UserSchema, repeated: true, root: true },
    { name: 'orders', number: 2, type: 'varint', message: OrderSchema, repeated: true, root: true },
    { name: 'archived', number: 3, type: 'varint', message: OrderSchema, repeated: true },
    { name: 'venue', number: 4, type: 'string' },
  ],
};

export function sampleBook(): Record<string, unknown> {
  return {
    users: [
      { user_id: 1n, name: 'alice', tier: 2n },
      { user_id: 2n, name: 'bob', tier: -1n },
    ],
    orders: [
      {
        order_id: 101n,
        symbol: 'BTC-USD',
        price: 6725000n,
        qty: 15n,
        user_id: 1n,
        fills: [5n, 6n, 4n],
        next: 102n,
      },
      {
        order_id: 102n,
        symbol: 'ETH-USD',
        price: 301250n,
        qty: -8n,
        user_id: 2n,
        fills: [8n],
      },
    ],
    archived: [],
    venue: 'XSHG',
  };
}
