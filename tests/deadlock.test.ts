import assert from 'node:assert/strict';
import { test } from 'node:test';
import { DataStore } from '../src/store.ts';
import { TransactionManager } from '../src/transactionManager.ts';
import { DeadlockError } from '../src/types.ts';

test('deadlock cycle is detected and youngest tx is aborted as victim', async () => {
  const tm = new TransactionManager(new DataStore({ a: 1, b: 1 }));
  const t1 = tm.begin();
  const t2 = tm.begin();

  await tm.write(t1, 'a', 10); // t1: X(a)
  await tm.write(t2, 'b', 20); // t2: X(b)

  const t1WantsB = tm.write(t1, 'b', 11); // t1 waits on t2
  await new Promise((resolve) => setImmediate(resolve));

  // t2 (younger) requesting 'a' closes the cycle -> t2 is the victim.
  await assert.rejects(tm.write(t2, 'a', 21), (error: unknown) => {
    assert.ok(error instanceof DeadlockError);
    assert.equal(error.victimTxId, t2.id);
    return true;
  });

  // Victim aborts: rollback + atomic release; t1 then completes.
  tm.abort(t2);
  assert.equal(tm.store.get('b'), 1, 't2 write on b rolled back');
  await t1WantsB;
  tm.commit(t1);
  assert.equal(tm.store.get('a'), 10);
  assert.equal(tm.store.get('b'), 11);
  assert.ok(tm.monitor.verify(), `violations: ${tm.monitor.violations}`);
});
