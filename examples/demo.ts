import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/aries/engine.ts';
import { RecoveryManager } from '../src/aries/recovery.ts';
import { readWal } from '../src/aries/wal.ts';
import { LocalityAwareRouter } from '../src/sharding/router.ts';
import type { NodeInfo } from '../src/sharding/topology.ts';

function part1Aries(): void {
  console.log('=== Part 1: ARIES crash recovery ===');
  const walPath = join(tmpdir(), `demo-aries-${process.pid}.wal`);
  const db = Database.create(walPath);
  for (const [page, value] of [['A', 100], ['B', 200], ['C', 300], ['D', 400], ['E', 500]] as const) {
    db.seedPage(page, value);
  }

  const t1 = db.begin();
  db.write(t1, 'A', 150);
  const t2 = db.begin();
  db.write(t2, 'B', 250);
  const checkpointLSN = db.checkpoint();
  db.write(t1, 'C', 350);
  db.commit(t1);
  const t3 = db.begin();
  db.write(t3, 'D', 450);
  db.write(t3, 'A', 999);
  db.write(t2, 'E', 550);
  db.commit(t2);
  const t4 = db.begin();
  db.write(t4, 'B', 777);
  const t5 = db.begin();
  db.write(t5, 'C', 888);
  db.write(t5, 'E', 111);

  db.flushPage('A');
  console.log(`checkpoint at LSN ${checkpointLSN}; committed: T${t1}, T${t2}; uncommitted at crash: T${t3}, T${t4}, T${t5}`);
  db.crash();
  console.log(`crashed. WAL (${walPath}) has ${readWal(walPath).length} records; disk A=${db.diskValue('A')} (uncommitted value was stolen to disk)`);

  const recovery = new RecoveryManager(walPath, db.diskPages);
  const result = recovery.recover();

  console.log('\n-- Analysis --');
  console.log('transaction table:', [...result.analysis.txnTable.values()]);
  console.log('dirty page table:', [...result.analysis.dirtyPageTable.entries()]);
  console.log('\n-- Redo (repeat history) --');
  console.log(`start LSN ${result.redo.startLSN}; applied=[${result.redo.applied}] skipped(already on disk)=[${result.redo.skipped}]`);
  console.log('\n-- Undo (losers, reverse scan with CLRs) --');
  console.log(`losers=[${result.undo.losers}] CLRs=[${result.undo.clrLSNs}] ended=[${result.undo.endedTxIds}] flushedPages=${result.undo.flushedPages}`);
  console.log('\n-- Recovered disk state --');
  for (const page of ['A', 'B', 'C', 'D', 'E']) {
    console.log(`page ${page} = ${db.diskValue(page)}`);
  }
  console.log('expected: A=150 (T1), B=250 (T2), C=350 (T1), D=400 (T3 undone), E=550 (T2)');
}

async function part2Sharding(): Promise<void> {
  console.log('\n=== Part 2: hierarchical sharding + locality-aware routing ===');
  const router = new LocalityAwareRouter(1024, 3);
  const nodes: NodeInfo[] = [];
  for (const region of ['us-east', 'eu-west', 'ap-south']) {
    for (const zone of ['z1', 'z2']) {
      for (let i = 0; i < 2; i++) {
        nodes.push({ id: `${region}-${zone}-n${i}`, region, zone, capacity: 1 });
      }
    }
  }
  for (const node of nodes) await router.addNode(node);

  const client = { region: 'eu-west', zone: 'z1' };
  const keys = Array.from({ length: 20000 }, (_, i) => `user:${i}`);
  const plan = router.routeBatch(keys, client);
  const stats = router.stats();
  console.log(`topology: ${nodes.length} nodes (3 regions x 2 zones x 2 nodes), 1024 virtual shards, 3 replicas`);
  console.log(`routed ${keys.length} keys from ${client.region}/${client.zone}:`);
  console.log(`  zone-local: ${stats.zoneLocal} (${((stats.zoneLocal / keys.length) * 100).toFixed(1)}%)`);
  console.log(`  region-local: ${stats.regionLocal}, remote: ${stats.remote}`);
  console.log(`  batching: ${keys.length} keys -> ${plan.nodeHops} node RPCs (vs ${keys.length} without batching)`);

  const before = router.route('user:42', client);
  router.setNodeHealth(before.nodeId, false);
  const after = router.route('user:42', client);
  console.log(`failover: user:42 ${before.nodeId} (cost ${before.cost}) -> ${after.nodeId} (cost ${after.cost})`);
  router.setNodeHealth(before.nodeId, true);

  const removed = await router.removeNode('ap-south-z2-n0');
  console.log(`rebalance: removing one node moved ${removed.moved} of 1024 shard owners (minimal movement)`);
  console.log('final stats:', router.stats());
}

part1Aries();
await part2Sharding();
