import test from 'node:test';
import assert from 'node:assert/strict';
import { ReceiptLostError, WorkflowInconclusiveError, WorkflowPausedError } from '../src/errors.ts';
import { assertConsistent, boot, makeWorld, terminalRecords } from './helpers.ts';

test('happy path: reserve -> charge -> schedule, terminal completed', async () => {
  const world = makeWorld();
  const { journal, executor } = boot(world);
  const terminal = await executor.run();

  assert.equal(terminal, 'completed');
  assertConsistent(world, 'completed');

  // Intent and result are journaled for every action.
  const intents = journal.records.filter((r) => r.type === 'intent' && r.data.phase === 'execute');
  const results = journal.records.filter((r) => r.type === 'result' && r.data.phase === 'execute');
  assert.deepEqual(intents.map((r) => r.data.step), ['reserve-inventory', 'charge-payment', 'schedule-delivery']);
  assert.equal(results.length, 3);
  assert.equal(terminalRecords(journal).length, 1);
  journal.close();
});

test('business failure at charge: only confirmed steps compensated, in reverse order', async () => {
  const world = makeWorld([{ method: 'payment.charge', occurrence: 1, fault: 'error' }]);
  const { journal, executor } = boot(world);
  const terminal = await executor.run();

  assert.equal(terminal, 'compensated');
  assertConsistent(world, 'compensated');

  // reserve-inventory succeeded and was released; charge failed; delivery never ran.
  const compensations = journal.records.filter((r) => r.type === 'result' && r.data.phase === 'compensate');
  assert.deepEqual(compensations.map((r) => r.data.step), ['reserve-inventory']);
  assert.equal(world.delivery.callCount('delivery.schedule'), 0);
  assert.equal(world.inventory.callCount('inventory.release'), 1);
  journal.close();
});

test('failure at delivery: both earlier steps compensated in reverse order', async () => {
  const world = makeWorld([{ method: 'delivery.schedule', occurrence: 1, fault: 'error' }]);
  const { journal, executor } = boot(world);
  const terminal = await executor.run();

  assert.equal(terminal, 'compensated');
  assertConsistent(world, 'compensated');

  const compensations = journal.records.filter((r) => r.type === 'result' && r.data.phase === 'compensate');
  assert.deepEqual(compensations.map((r) => r.data.step), ['charge-payment', 'reserve-inventory']);
  journal.close();
});

test('lost receipt: recovery queries by idempotency key instead of redoing blindly', async () => {
  const world = makeWorld([{ method: 'payment.charge', occurrence: 1, fault: 'lost-receipt' }]);

  const first = boot(world);
  await assert.rejects(first.executor.run(), WorkflowInconclusiveError);
  // The charge actually committed in the payment system; the journal does not know yet.
  assert.equal(world.payment.charges.get(world.order.orderId), world.order.amount);
  assert.equal(
    first.journal.records.filter((r) => r.type === 'result' && r.data.step === 'charge-payment').length,
    0,
    'no result journaled for the unknown outcome',
  );
  first.journal.close();

  // "Restart" with a healthy network: recovery must reconcile, not re-charge.
  world.payment.setFaultScript([]);
  const second = boot(world);
  const terminal = await second.executor.run();

  assert.equal(terminal, 'completed');
  assertConsistent(world, 'completed');
  assert.equal(world.payment.callCount('payment.charge'), 1, 'charge executed exactly once');
  const reconciled = second.journal.records.find((r) => r.type === 'result' && r.data.step === 'charge-payment');
  assert.equal(reconciled?.data.reconciled, true, 'result recovered via idempotency-key query');
  second.journal.close();
});

test('lost receipt on a compensation is reconciled by key, not re-applied', async () => {
  const world = makeWorld([
    { method: 'delivery.schedule', occurrence: 1, fault: 'error' },
    { method: 'payment.refund', occurrence: 1, fault: 'lost-receipt' },
  ]);

  const first = boot(world);
  await assert.rejects(first.executor.run(), WorkflowInconclusiveError);
  // The refund committed in the payment system but the journal stopped.
  assert.equal(world.payment.balances.get(world.order.account), 1000);
  first.journal.close();

  world.payment.setFaultScript([{ method: 'delivery.schedule', occurrence: 1, fault: 'error' }]);
  const second = boot(world);
  const terminal = await second.executor.run();

  assert.equal(terminal, 'compensated');
  assertConsistent(world, 'compensated');
  assert.equal(world.payment.callCount('payment.refund'), 1, 'refund applied exactly once');
  second.journal.close();
});

test('transient compensation failure is recoverable; done compensations are not re-applied', async () => {
  const world = makeWorld([
    { method: 'delivery.schedule', occurrence: 1, fault: 'error' },
    { method: 'payment.refund', occurrence: 1, fault: 'transient' },
  ]);

  const first = boot(world);
  await assert.rejects(first.executor.run(), WorkflowPausedError);
  // Refund did not happen yet; release not even started (reverse order).
  assert.equal(world.payment.balances.get(world.order.account), 1000 - world.order.amount);
  assert.equal(world.inventory.callCount('inventory.release'), 0);
  first.journal.close();

  // Recover: refund now succeeds, then release runs exactly once.
  world.payment.setFaultScript([{ method: 'delivery.schedule', occurrence: 1, fault: 'error' }]);
  const second = boot(world);
  const terminal = await second.executor.run();

  assert.equal(terminal, 'compensated');
  assertConsistent(world, 'compensated');
  assert.equal(world.payment.callCount('payment.refund'), 2, 'one failed attempt + one success');
  assert.equal(world.inventory.callCount('inventory.release'), 1);

  // Re-running a finished workflow changes nothing.
  const again = await second.executor.run();
  assert.equal(again, 'compensated');
  assert.equal(world.inventory.callCount('inventory.release'), 1, 'no repeated compensation');
  assert.equal(world.payment.callCount('payment.refund'), 2);
  second.journal.close();
});

test('transient execute failure pauses and resumes without double-applying', async () => {
  const world = makeWorld([{ method: 'payment.charge', occurrence: 1, fault: 'transient' }]);

  const first = boot(world);
  await assert.rejects(first.executor.run(), WorkflowPausedError);
  first.journal.close();

  const second = boot(world);
  const terminal = await second.executor.run();

  assert.equal(terminal, 'completed');
  assertConsistent(world, 'completed');
  assert.equal(world.payment.callCount('payment.charge'), 2);
  assert.equal(world.payment.balances.get(world.order.account), 1000 - world.order.amount);
  second.journal.close();
});

test('ReceiptLostError carries the idempotency key for operators', () => {
  const error = new ReceiptLostError('order:O-1:charge-payment');
  assert.match(error.message, /order:O-1:charge-payment/);
});
