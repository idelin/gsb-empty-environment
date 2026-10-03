import { BPlusTree, OVERFLOW_PREFIX_BYTES, PAGE_SIZE } from '../src/index.ts';

const KB = 1024;
const seed =
  'The quick brown fox jumps over the lazy dog. 溢出页链式挂接器演示文本。0123456789abcdef\n';
const text64k = seed.repeat(Math.ceil((64 * KB) / seed.length)).slice(0, 64 * KB);

const tree = new BPlusTree();
tree.insert(Buffer.from('row:0001'), Buffer.from(text64k, 'utf8'));
tree.insert(Buffer.from('row:0002'), Buffer.from('a small inline value'));

const desc = tree.describeEntry(Buffer.from('row:0001'));
console.log(`page size                 : ${PAGE_SIZE} bytes`);
console.log(`stored value size         : ${desc.totalLength} bytes (64KB)`);
console.log(`leaf keeps inline prefix  : ${desc.prefix!.length} bytes`);
console.log(`leaf prefix preview       : ${JSON.stringify(desc.prefix!.toString('utf8', 0, 48))} ...`);
console.log(`overflow chain head page  : ${desc.firstOverflowPageId}`);
console.log(`overflow chain page ids   : [${desc.overflowChainPageIds.join(', ')}]`);
console.log(`overflow chain length     : ${desc.overflowChainPageIds.length} pages`);

const restored = tree.get(Buffer.from('row:0001'))!;
console.log(`reassembled value size    : ${restored.length} bytes`);
console.log(`round-trip identical      : ${restored.toString('utf8') === text64k}`);
console.log(`small row stays inline    : ${tree.get(Buffer.from('row:0002'))!.toString()}`);
console.log(`prefix bytes per leaf rec : ${OVERFLOW_PREFIX_BYTES}`);
console.log('stats                     :', tree.stats());
