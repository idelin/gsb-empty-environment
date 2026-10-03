/**
 * Runnable demo: `npm run example`
 * 1. Encode a nested protobuf message by hand, print the wire bytes, decode it back.
 * 2. Run a saga whose last step fails and watch the reverse compensation roll back.
 */
import { decodeMessage, encodeMessage } from '../src/schema.ts';
import type { MessageSchema } from '../src/schema.ts';
import { TruncationError } from '../src/wire.ts';
import { Saga } from '../src/saga.ts';

const addressSchema: MessageSchema = {
  name: 'Address',
  fields: {
    1: { name: 'city', type: 'string' },
    2: { name: 'zip', type: 'fixed32' },
    3: { name: 'coords', type: 'sfixed64', repeated: true, packed: true },
  },
};

const orderSchema: MessageSchema = {
  name: 'Order',
  fields: {
    1: { name: 'id', type: 'uint64' },
    2: { name: 'note', type: 'string' },
    3: { name: 'balance', type: 'sint64' },
    4: { name: 'scores', type: 'int32', repeated: true, packed: true },
    5: { name: 'address', type: 'message', message: addressSchema },
    6: { name: 'vip', type: 'bool' },
  },
};

const order = {
  id: 9007199254740993n,
  note: 'hello, 世界',
  balance: -4200n,
  scores: [7, -3, 128],
  address: { city: '上海', zip: 200000, coords: [121473701n, 31161180n] },
  vip: true,
};

console.log('=== 1. Protobuf wire round-trip ===');
const bytes = encodeMessage(orderSchema, order);
console.log(`wire bytes (${bytes.length}):`, Buffer.from(bytes).toString('hex'));
const decoded = decodeMessage(orderSchema, bytes);
console.log('decoded:', JSON.stringify(decoded, (_, v) => (typeof v === 'bigint' ? `${v}n` : v), 2));

console.log('\n=== 2. Truncation guard ===');
try {
  decodeMessage(orderSchema, bytes.subarray(0, bytes.length - 4));
} catch (error) {
  if (error instanceof TruncationError) {
    console.log('caught TruncationError:', error.message);
  } else {
    throw error;
  }
}

console.log('\n=== 3. Saga rollback (reverse compensation) ===');
interface BankCtx {
  ledger: string[];
}
const ctx: BankCtx = { ledger: [] };

function bankStep(name: string, action: string, undo: string, fail = false) {
  return {
    name,
    execute: (c: BankCtx) => {
      c.ledger.push(`+ ${action}`);
      if (fail) throw new Error(`${name}: downstream rejected the transfer`);
    },
    compensate: (c: BankCtx) => {
      c.ledger.push(`- ${undo}`);
    },
  };
}

const saga = new Saga<BankCtx>();
saga
  .addStep(bankStep('debit-alice', 'debit alice 100', 'refund alice 100'))
  .addStep(bankStep('credit-bob', 'credit bob 100', 'claw back bob 100'))
  .addStep(bankStep('notify', 'send notification', 'retract notification', true));

saga.onEvent((event) => console.log(`  [event] ${event.type}${event.step ? ` (${event.step})` : ''}`));
const status = await saga.run(ctx);
console.log('final status:', status);
console.log('ledger:', ctx.ledger);
