import test from 'node:test';
import assert from 'node:assert/strict';
import { SimulatedCrashError } from '../src/errors.ts';
import { assertConsistent, boot, makeWorld } from './helpers.ts';

/** Count the durable records of one fault-free run (the persistence boundaries). */
async function countBoundaries(): Promise<number> {
  const world = makeWorld();
  const { journal, executor } = boot(world);
  await executor.run();
  const total = journal.records.length;
  journal.close();
  return total;
}

test('restart at every persistence boundary still completes consistently', async (t) => {
  const boundaries = await countBoundaries();
  assert.equal(boundaries, 8, 'started + 3x(intent,result) + terminal');

  // crashAfter = number of records durable before the crash; valid range is
  // 0..boundaries-1 (crashing "after the last record" is a clean shutdown).
  for (let crashAfter = 0; crashAfter < boundaries; crashAfter++) {
    await t.test(`crash after ${crashAfter} durable record(s)`, async () => {
      const world = makeWorld();
      const crashed = boot(world, {
        beforeAppend: (nextSeq) => {
          if (nextSeq === crashAfter) throw new SimulatedCrashError();
        },
      });
      await assert.rejects(crashed.executor.run(), SimulatedCrashError);
      crashed.journal.close();

      // "Restart the process": new journal + executor, same files and adapters.
      const recovered = boot(world);
      const terminal = await recovered.executor.run();
      assert.equal(terminal, 'completed');
      assertConsistent(world, 'completed');
      recovered.journal.close();
    });
  }
});

test('a chain of crashes across restarts still converges', async () => {
  const world = makeWorld();

  for (const crashAfter of [1, 3, 5]) {
    const crashed = boot(world, {
      beforeAppend: (nextSeq) => {
        if (nextSeq === crashAfter) throw new SimulatedCrashError();
      },
    });
    await assert.rejects(crashed.executor.run(), SimulatedCrashError);
    crashed.journal.close();
  }

  const recovered = boot(world);
  const terminal = await recovered.executor.run();
  assert.equal(terminal, 'completed');
  assertConsistent(world, 'completed');
  recovered.journal.close();
});

test('crash in the middle of compensation resumes compensation, not the forward pass', async () => {
  const faults = [{ method: 'delivery.schedule', occurrence: 1, fault: 'error' as const }];
  const world = makeWorld(faults);

  // First process: fails at delivery, crashes while journaling the refund result.
  const crashed = boot(world, {
    beforeAppend: (_nextSeq, record) => {
      if (record.type === 'result' && record.data.step === 'charge-payment' && record.data.phase === 'compensate') {
        throw new SimulatedCrashError();
      }
    },
  });
  await assert.rejects(crashed.executor.run(), SimulatedCrashError);
  crashed.journal.close();

  // The refund committed in the payment system; the journal never saw the result.
  assert.equal(world.payment.balances.get(world.order.account), 1000);
  assert.equal(world.inventory.stock.get(world.order.sku), 10 - world.order.quantity, 'release not done yet');

  const recovered = boot(world);
  const terminal = await recovered.executor.run();
  assert.equal(terminal, 'compensated');
  assertConsistent(world, 'compensated');
  assert.equal(world.payment.callCount('payment.refund'), 1, 'refund not re-applied after recovery');
  assert.equal(world.inventory.callCount('inventory.release'), 1);
  recovered.journal.close();
});
