import { Aggregator, verifyCrossShardProof } from '../src/index.ts';

/**
 * Acceptance scenario:
 *  - 4 independent shard trees, each maintaining its own local root.
 *  - An account in shard 2 initiates a transfer.
 *  - Shard 2 produces an internal inclusion proof.
 *  - The master aggregator attaches shard 2's position proof in the Meta-Tree.
 *  - An arbitrary external verifier independently validates the combined proof.
 */

const aggregator = new Aggregator(); // 4 shards: 0..3

// Seed accounts across shards.
aggregator.shard(0).setAccount('carol', { balance: 500n, nonce: 0 });
aggregator.shard(1).setAccount('dave', { balance: 300n, nonce: 0 });
aggregator.shard(2).setAccount('alice', { balance: 1000n, nonce: 0 });
aggregator.shard(2).setAccount('bob', { balance: 250n, nonce: 0 });
aggregator.shard(3).setAccount('erin', { balance: 800n, nonce: 0 });

console.log('=== Shard local roots (before transfer) ===');
for (const shard of aggregator.shards) {
  console.log(`  shard ${shard.shardId}: ${shard.root().toString('hex')}`);
}
console.log(`  meta root: ${aggregator.metaRoot().toString('hex')}\n`);

// Account in shard 2 initiates a transfer.
console.log('>>> shard 2: alice transfers 400 to bob');
aggregator.shard(2).applyTransfer('alice', 'bob', 400n);

const alice = aggregator.shard(2).getAccount('alice');
const bob = aggregator.shard(2).getAccount('bob');
console.log(`    alice: balance=${alice.balance} nonce=${alice.nonce}`);
console.log(`    bob:   balance=${bob.balance} nonce=${bob.nonce}\n`);

console.log('=== Shard local roots (after transfer) ===');
for (const shard of aggregator.shards) {
  console.log(`  shard ${shard.shardId}: ${shard.root().toString('hex')}`);
}
const metaRoot = aggregator.metaRoot().toString('hex');
console.log(`  meta root: ${metaRoot}\n`);

// Shard 2 internal inclusion proof + Meta-Tree position proof => combined proof.
const proof = aggregator.buildCrossShardProof(2, 'alice');
console.log('=== Combined cross-shard proof for alice (shard 2) ===');
console.log(JSON.stringify(proof, null, 2));
console.log();

// Arbitrary external verifier: only needs the proof and the trusted meta root.
const ok = verifyCrossShardProof(proof, metaRoot);
console.log(`external verification (pinned to meta root): ${ok ? 'PASS' : 'FAIL'}`);

const okUnpinned = verifyCrossShardProof(proof);
console.log(`external verification (self-consistent only): ${okUnpinned ? 'PASS' : 'FAIL'}`);

// Negative check: a tampered state must fail.
const tampered = structuredClone(proof);
tampered.accountState.balance = '999999';
console.log(`tampered proof rejected: ${!verifyCrossShardProof(tampered, metaRoot) ? 'PASS' : 'FAIL'}`);

if (!ok || !okUnpinned) process.exit(1);
