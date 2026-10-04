import test from 'node:test';
import assert from 'node:assert/strict';
import { assertConsistent, boot, makeWorld, terminalRecords } from './helpers.ts';

test('cancel before start: no side effects, terminal cancelled', async () => {
  const world = makeWorld();
  const { journal, executor } = boot(world);

  assert.equal(executor.cancel(), 'requested');
  const terminal = await executor.run();

  assert.equal(terminal, 'cancelled');
  assertConsistent(world, 'cancelled');
  assert.equal(world.inventory.callCount('inventory.reserve'), 0, 'no action ever executed');
  assert.equal(world.payment.callCount('payment.charge'), 0);
  assert.equal(world.delivery.callCount('delivery.schedule'), 0);
  assert.equal(terminalRecords(journal).length, 1);
  journal.close();
});

test('cancel mid-flight: completed steps are compensated, later steps never run', async () => {
  const world = makeWorld();
  const { journal, executor } = boot(world, {}, {
    afterStep: (i: number) => {
      if (i === 0) executor.cancel('customer changed their mind');
    },
  });
  const terminal = await executor.run();

  assert.equal(terminal, 'cancelled');
  assertConsistent(world, 'cancelled');
  assert.equal(world.inventory.callCount('inventory.reserve'), 1);
  assert.equal(world.inventory.callCount('inventory.release'), 1, 'reservation undone');
  assert.equal(world.payment.callCount('payment.charge'), 0, 'charge never attempted');
  assert.equal(world.delivery.callCount('delivery.schedule'), 0);
  assert.equal(terminalRecords(journal).length, 1);
  journal.close();
});

test('cancel racing the final success: cancel first -> cancelled, exactly one terminal', async () => {
  const world = makeWorld();
  const { journal, executor } = boot(world, {}, {
    afterStep: (i: number) => {
      if (i === 2) executor.cancel(); // lands after the last action, before the terminal write
    },
  });
  const terminal = await executor.run();

  assert.equal(terminal, 'cancelled');
  assertConsistent(world, 'cancelled');
  assert.equal(terminalRecords(journal).length, 1, 'exactly one terminal record');
  assert.equal(world.delivery.shipments.get(world.order.orderId)?.status, 'cancelled');
  journal.close();
});

test('cancel racing the final success: success first -> completed, cancel is a no-op', async () => {
  const world = makeWorld();
  const { journal, executor } = boot(world);
  const terminal = await executor.run();
  assert.equal(terminal, 'completed');

  // The cancel loses the race: the terminal state already exists.
  assert.equal(executor.cancel(), 'completed');
  assert.equal(await executor.run(), 'completed');

  assertConsistent(world, 'completed');
  assert.equal(terminalRecords(journal).length, 1, 'exactly one terminal record');
  assert.equal(journal.records.filter((r) => r.type === 'cancel').length, 0, 'cancel not even journaled');
  journal.close();
});

test('repeated cancellation is idempotent: one cancel record, one terminal', async () => {
  const world = makeWorld();
  const { journal, executor } = boot(world);

  assert.equal(executor.cancel(), 'requested');
  assert.equal(executor.cancel(), 'requested');
  const terminal = await executor.run();
  assert.equal(terminal, 'cancelled');
  assert.equal(executor.cancel(), 'cancelled');
  assert.equal(await executor.run(), 'cancelled');

  assertConsistent(world, 'cancelled');
  assert.equal(journal.records.filter((r) => r.type === 'cancel').length, 1, 'cancel journaled once');
  assert.equal(terminalRecords(journal).length, 1, 'terminal written once');
  journal.close();
});

test('cancel requested while a previous process was running is honored after restart', async () => {
  const world = makeWorld();
  const first = boot(world);
  first.executor.cancel('operator abort');
  first.journal.close(); // process "dies" before run() gets anywhere

  const second = boot(world);
  const terminal = await second.executor.run();
  assert.equal(terminal, 'cancelled');
  assertConsistent(world, 'cancelled');
  assert.equal(world.inventory.callCount('inventory.reserve'), 0);
  second.journal.close();
});
