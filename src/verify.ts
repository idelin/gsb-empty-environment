import { runExhaustive } from './exhaustive.ts';

const startedAt = Date.now();
const stats = runExhaustive();
const elapsed = ((Date.now() - startedAt) / 1000).toFixed(2);

console.log('Exhaustive verification passed.');
console.log(`  base strings:        ${stats.strings}`);
console.log(`  patches per string:  ~${Math.round(stats.patches / stats.strings)}`);
console.log(`  patch pairs checked: ${stats.pairs} (x2 id orderings included)`);
console.log(`  conflict-free pairs: ${stats.conflictFreePairs}`);
console.log(`  conflicted pairs:    ${stats.conflictedPairs}`);
console.log(`  edits rebased:       ${stats.editsRebased}`);
console.log(`  edits unpreserved:   ${stats.editsUnpreserved}`);
console.log(`  elapsed:             ${elapsed}s`);
