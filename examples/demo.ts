import { LockMonitor } from '../src/lock-monitor.ts';
import { TransactionManager } from '../src/transaction-manager.ts';
import { DeadlockError } from '../src/errors.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const tm = new TransactionManager();
tm.lockManager.onAudit((event) => {
  const txn = tm.get(event.txnId);
  console.log(
    `  [lock] ${event.kind.padEnd(7)} txn=${event.txnId} ${event.mode.padEnd(9)} on [${event.resource}] (txn state: ${txn?.phase ?? 'n/a'})`,
  );
});
const monitor = new LockMonitor(tm.lockManager, (id) => tm.get(id)?.phase);

async function transferScenario(): Promise<void> {
  console.log('--- Scenario 1: concurrent transfers over contended accounts ---');
  tm.store.write('alice', 100);
  tm.store.write('bob', 100);
  tm.store.write('carol', 100);

  const transfer = async (from: string, to: string, amount: number, delay: number) => {
    for (let attempt = 1; ; attempt++) {
      const txn = tm.begin();
      console.log(`txn ${txn.id}: transfer ${amount} from ${from} to ${to} (attempt ${attempt})`);
      try {
        const fromBalance = (await txn.read(from))!;
        await sleep(delay);
        await txn.write(from, fromBalance - amount);
        const toBalance = (await txn.read(to))!;
        await txn.write(to, toBalance + amount);
        txn.commit();
        console.log(`txn ${txn.id}: committed with commitTimestamp=${txn.commitTimestamp}`);
        return;
      } catch (err) {
        if (!(err instanceof DeadlockError)) throw err;
        console.log(`txn ${txn.id}: chosen as deadlock victim -> aborted, writes rolled back, retrying`);
      }
    }
  };

  await Promise.all([
    transfer('alice', 'bob', 30, 10),
    transfer('bob', 'carol', 20, 25),
    transfer('carol', 'alice', 10, 40),
  ]);

  console.log('commit order (serialization order):', tm.commitOrder());
  console.log('final balances:', tm.store.snapshot());
}

async function crashScenario(): Promise<void> {
  console.log('--- Scenario 2: injected crash mid-transaction ---');
  const t1 = tm.begin();
  await t1.write('alice', 5);
  await t1.write('dave', 500); // uncommitted insert
  console.log(`txn ${t1.id}: wrote alice=5, dave=500 (uncommitted), holding:`, [
    ...tm.lockManager.heldResources(t1.id).keys(),
  ]);

  const t2 = tm.begin();
  const blockedRead = t2.read('alice');
  await sleep(20);
  console.log(`txn ${t2.id}: read of alice is blocked behind txn ${t1.id}`);

  console.log(`>>> injecting crash into txn ${t1.id}`);
  tm.injectCrash(t1.id);
  console.log(`txn ${t1.id}: state=${t1.phase} reason=${t1.abortReason}, locks left:`, tm.lockManager.heldResources(t1.id).size);

  console.log(`txn ${t2.id}: read of alice unblocked, value =`, await blockedRead);
  t2.commit();
  console.log('store after recovery:', tm.store.snapshot());
}

await transferScenario();
console.log('');
await crashScenario();
monitor.assertNoEarlyRelease();
console.log('');
console.log('OK: no lock was ever released before commit/abort (SS2PL verified).');
