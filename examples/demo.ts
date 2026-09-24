import { RichTextDocument } from '../src/index.ts';

const show = (title: string, doc: RichTextDocument) => {
  console.log(`\n=== ${title} ===`);
  console.log('text     :', JSON.stringify(doc.text));
  console.log('canonical:', doc.toCanonicalJSON());
};

// 1. Adjacent bold ranges merge.
const bold = new RichTextDocument('abcdefghij');
bold.addBold(0, 5);
bold.addBold(5, 10);
show('1. adjacent bolds merge', bold);

// 2. A new link overrides the overlap; old link remnants survive.
const link = new RichTextDocument('hello world');
link.addLink(0, 10, '/old');
link.addLink(4, 8, '/new');
show('2. link override keeps remnants', link);

// 3. Editing inside an indivisible entity invalidates it with a reason.
const entity = new RichTextDocument('hello @alice world');
entity.addEntity(6, 12, 'mention', { id: 'u1' });
const result = entity.insert(9, 'x');
show('3. entity interior edit invalidates', entity);
console.log('invalidations:', JSON.stringify(result.invalidations, null, 2));

// 4. UTF-16 offsets never split a surrogate pair.
const emoji = new RichTextDocument('a\u{1F600}b');
console.log('\n=== 4. surrogate pair safety ===');
console.log('snapPoint(2)   :', emoji.snapPoint(2), '(mid-pair offset 2 -> 1)');
console.log('snapRangeEnd(2):', emoji.snapRangeEnd(2), '(mid-pair offset 2 -> 3)');
emoji.insert(2, 'x');
show('insert at mid-pair offset', emoji);

// 5. LF and CRLF line endings (CRLF = 2 UTF-16 units).
const lines = new RichTextDocument('ab\r\ncd\nef');
lines.addBold(1, 5); // "b\r\nc"
lines.remove(2, 4); // delete "\r\n"
show('5. LF + CRLF edits', lines);

// 6. Canonical serialization is insertion-order independent.
const a = new RichTextDocument('abcdefghijkl');
a.addBold(0, 5);
a.addLink(2, 6, '/x');
a.addBold(6, 9);
a.addEntity(10, 12, 'tag');

const b = new RichTextDocument('abcdefghijkl');
b.addEntity(10, 12, 'tag');
b.addBold(6, 9);
b.addLink(2, 6, '/x');
b.addBold(0, 5);

console.log('\n=== 6. insertion-order independence ===');
console.log('doc A:', a.toCanonicalJSON());
console.log('doc B:', b.toCanonicalJSON());
console.log('identical:', a.toCanonicalJSON() === b.toCanonicalJSON());
