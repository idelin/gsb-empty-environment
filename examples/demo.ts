import { BTree } from '../src/btree.ts';

const KB = 1024;

function makeText(bytes: number): string {
  const unit =
    'The quick brown fox jumps over the lazy dog. 0123456789 abcdefghijklmnop.\n';
  return unit.repeat(Math.ceil(bytes / unit.length)).slice(0, bytes);
}

const tree = new BTree();

// a few normal rows
tree.insert('user:1:name', 'Alice');
tree.insert('user:1:email', 'alice@example.com');

// one row with a 64KB long text field
const bigText = makeText(64 * KB);
tree.insert('user:1:bio', bigText);

console.log('=== B+ tree with chained overflow pages ===');
console.log('stats:', tree.stats());

const info = tree.inspectEntry('user:1:bio')!;
console.log('\n--- leaf entry for "user:1:bio" ---');
console.log('value stored inline :', info.valueInline);
console.log('value total length  :', info.valueTotalLen, 'bytes');
console.log('prefix kept in leaf :', info.valuePrefix.length, 'bytes');
console.log('prefix preview      :', JSON.stringify(info.valuePrefix.toString().slice(0, 60)) + '...');
console.log('overflow page chain :', info.valueOverflowChain.join(' -> '), '-> 0 (end)');
console.log('overflow page count :', info.valueOverflowChain.length);

const restored = tree.get('user:1:bio')!;
console.log('\n--- transparent read ---');
console.log('restored length     :', restored.length, 'bytes');
console.log('round-trip identical:', restored.toString() === bigText);

console.log('\nsmall rows still inline:',
  tree.inspectEntry('user:1:name')!.valueInline === true &&
  tree.get('user:1:name')!.toString() === 'Alice');
