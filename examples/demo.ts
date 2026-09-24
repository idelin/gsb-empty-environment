/**
 * Demo: two sites edit a shared document concurrently.
 * Run with: node examples/demo.ts   (Node >= 22.6, type stripping)
 */
import { del, insert, Replica } from '../src/index.ts';

const doc = 'The quick\r\nbrown fox\njumps \u{1F980} over';
console.log('initial document:', JSON.stringify(doc));

const alice = new Replica(1, doc);
const bob = new Replica(2, doc);

// Both sites edit concurrently, based on the same version 0.
const fromAlice = alice.local(insert(4, 'very ')); // "The very quick..."
const fromBob = bob.local(del(11, 6)); // delete "brown "

console.log('\nalice inserts "very " at 4   ->', JSON.stringify(alice.text));
console.log('bob deletes [11, 17) "brown " ->', JSON.stringify(bob.text));

// Exchange ops in opposite delivery orders; both must converge.
alice.receive(fromBob);
bob.receive(fromAlice);

console.log('\nafter exchanging ops:');
console.log('alice:', JSON.stringify(alice.text), `(version ${alice.version})`);
console.log('bob:  ', JSON.stringify(bob.text), `(version ${bob.version})`);
console.log('converged:', alice.text === bob.text);

// Invalid base versions are rejected.
try {
  alice.receive({ baseVersion: 99, siteId: 2, op: insert(0, '?') });
} catch (err) {
  console.log('\nrejected stale/future base version:', (err as Error).message);
}

// Offsets never split surrogate pairs.
try {
  alice.local(del(26, 1)); // would cut the crab emoji in half
} catch (err) {
  console.log('rejected pair-splitting delete:', (err as Error).message);
}
