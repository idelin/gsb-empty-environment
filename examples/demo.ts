import { DataStore } from '../src/store.ts';
import { TransactionManager } from '../src/transactionManager.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

function showLocks(tm: TransactionManager, label: string): void {
  const snap = tm.locks.snapshot();
  const parts = Object.entries(snap)
    .map(([res, info]) => {
      const holders = Object.entries(info.holders).map(([tx, m]) => `${tx}:${m}`).join(',');
      const waiting = info.waiting.length ? ` waiting=[${info.waiting.map((id) => 'tx' + id)}]` : '';
      return `${res}{${holders}}${waiting}`;
    });
  console.log(`  [locks] ${label}: ${parts.length ? parts.join('  ') : '(empty)'}`);
}

async function main(): Promise<void> {
  console.log('=== SS2PL demo: transfer between accounts ===\n');
  const tm = new TransactionManager(new DataStore({ alice: 1000, bob: 500 }));

  // --- Scenario 1: two concurrent transfers, strict 2PL serialization ---
  console.log('-- Scenario 1: concurrent transfers (growing phase, no early release)');
  const t1 = tm.begin();
  const t2 = tm.begin();

  await tm.write(t1, 'alice', 900); // t1: -100 from alice
  showLocks(tm, 't1 wrote alice, still ACTIVE');
  console.log(`  monitor: tx${t1.id} growing=${tm.monitor.isGrowing(t1.id)}, held=[${[...tm.monitor.heldLocks(t1.id).keys()]}]`);

  const t2Blocked = tm.read(t2, 'alice'); // t2 wants to audit alice -> blocks
  let t2Saw: number | undefined;
  void t2Blocked.then((v) => { t2Saw = v; });
  await sleep(10);
  console.log(`  t2 trying to read alice... blocked=${t2Saw === undefined}`);
  showLocks(tm, 't2 queued behind t1');

  await tm.write(t1, 'bob', 600); // t1: +100 to bob — STILL holds alice!
  showLocks(tm, 't1 grew to bob without releasing alice');
  tm.commit(t1); // shrinking point: everything released at once
  showLocks(tm, 'after t1 commit (atomic release)');
  await t2Blocked;
  console.log(`  t2 unblocked, reads alice=${t2Saw} (committed value)`);
  tm.commit(t2);

  // --- Scenario 2: crash injection + recovery ---
  console.log('\n-- Scenario 2: crash injection and recovery');
  const t3 = tm.begin();
  await tm.write(t3, 'alice', 0);   // t3 zeroes alice
  await tm.write(t3, 'bob', 1500);  // and inflates bob
  console.log(`  store after t3 dirty writes: ${JSON.stringify(tm.store.entries())}`);
  tm.crash(t3);
  console.log(`  *** tx${t3.id} CRASHED *** locks still held: ${tm.locks.heldBy(t3.id).size}`);
  showLocks(tm, 'post-crash (locks NOT lost)');
  const recovered = tm.recover();
  console.log(`  recovery: rolled back tx=[${recovered}], store=${JSON.stringify(tm.store.entries())}`);
  showLocks(tm, 'post-recovery');

  // --- Scenario 3: deadlock detection ---
  console.log('\n-- Scenario 3: deadlock detection');
  const t4 = tm.begin();
  const t5 = tm.begin();
  await tm.write(t4, 'alice', 950);
  await tm.write(t5, 'bob', 550);
  const t4WantsBob = tm.write(t4, 'bob', 540);
  await sleep(10);
  try {
    await tm.write(t5, 'alice', 940); // closes the cycle
    console.log('  unexpected: no deadlock detected');
  } catch (error) {
    console.log(`  deadlock detected: ${(error as Error).message}`);
    tm.abort(t5);
    console.log(`  victim tx${t5.id} aborted, bob restored to ${tm.store.get('bob')}`);
  }
  await t4WantsBob;
  tm.commit(t4);

  // --- Final verification ---
  console.log('\n-- Verification');
  console.log(`  shrink-phase violations: ${tm.monitor.violations.length}`);
  const { ok, conflicts } = tm.history.verifyCommitOrderEquivalence();
  console.log(`  schedule == commit-timestamp order: ${ok}`);
  if (!ok) console.log(`  conflicts: ${conflicts}`);
  console.log(`  final store: ${JSON.stringify(tm.store.entries())}`);
}

await main();
