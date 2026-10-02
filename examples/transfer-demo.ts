import { Aggregator, verifyCrossShardProof } from '../src/aggregator.ts';

const short = (hex: string): string => `${hex.slice(0, 12)}…`;

// --- 1. Boot the system: 4 independent shards + main aggregator -----------
const aggregator = new Aggregator(4);

aggregator.getShard(0).upsertAccount({ id: 'alice', balance: 500 });
aggregator.getShard(1).upsertAccount({ id: 'bob', balance: 800 });
aggregator.getShard(2).upsertAccount({ id: 'carol', balance: 1000 });
aggregator.getShard(2).upsertAccount({ id: 'dave', balance: 300 });
aggregator.getShard(3).upsertAccount({ id: 'erin', balance: 150 });
aggregator.syncAllShards();

console.log('=== Initial state ===');
for (const shard of aggregator.shards) {
  console.log(`shard ${shard.shardId} local root: ${short(shard.getLocalRoot())}`);
}
console.log(`meta root: ${short(aggregator.getMetaRoot())}\n`);

// --- 2. A transfer happens inside shard 2 ---------------------------------
const shard2 = aggregator.getShard(2);
console.log('=== Transfer in shard 2: carol -> dave, 250 ===');
console.log(`before: carol=${shard2.getAccount('carol')!.balance} dave=${shard2.getAccount('dave')!.balance}`);
const rootBefore = shard2.getLocalRoot();
shard2.applyTransfer('carol', 'dave', 250);
console.log(`after:  carol=${shard2.getAccount('carol')!.balance} dave=${shard2.getAccount('dave')!.balance}`);
console.log(`shard 2 root changed: ${short(rootBefore)} -> ${short(shard2.getLocalRoot())}`);
aggregator.syncShard(2);
const trustedMetaRoot = aggregator.getMetaRoot();
console.log(`meta root updated:   ${short(trustedMetaRoot)}\n`);

// --- 3. Aggregator merges shard proof + meta proof -------------------------
const proof = aggregator.buildCrossShardProof(2, 'dave');
console.log('=== Combined cross-shard proof for "dave" in shard 2 ===');
console.log(JSON.stringify(proof, null, 2));
console.log();

// --- 4. External verifier: only sees the JSON proof + trusted meta root ---
const wireFormat = JSON.stringify(proof); // proof is fully portable
const received = JSON.parse(wireFormat);
const ok = verifyCrossShardProof(received, trustedMetaRoot);
console.log(`external verification (pinned to trusted meta root): ${ok ? 'PASS' : 'FAIL'}`);

const stale = verifyCrossShardProof(received, '0'.repeat(64));
console.log(`verification against a stale meta root:                ${stale ? 'PASS' : 'FAIL (expected)'}`);

if (!ok) process.exit(1);
