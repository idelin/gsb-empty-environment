import { History } from '../src/index.ts';
import type { Relocation } from '../src/index.ts';

function show(title: string, r: Relocation): void {
  console.log(`\n=== ${title} ===`);
  console.dir(r, { depth: 8 });
}

// 1. Certain: identity tracking across consecutive edits (UTF-16 offsets).
{
  const h = new History('hello world foo');
  const diag = h.bind({ range: { start: 6, end: 11 }, message: 'world' });
  h.commit([{ kind: 'insert', at: 0, text: 'say ' }]);
  h.commit([{ kind: 'insert', at: 4, text: '\u{1F600} ' }]); // emoji = 2 UTF-16 units
  console.log(`text: ${JSON.stringify(h.text())}`);
  show('certain (identity): insertions before the target shift it precisely', h.relocate(diag));
}

// 2. Certain: whole-block move with a stable id.
{
  const h = new History('one two three');
  const diag = h.bind({ range: { start: 4, end: 7 }, message: 'two' });
  h.commit([{ kind: 'move', start: 4, end: 7, to: 0, moveId: 'mv-1' }]);
  console.log(`text: ${JSON.stringify(h.text())}`);
  show('certain (identity): move with stable id is followed', h.relocate(diag));
}

// 3. Invalid: full deletion is a tombstone; identical text elsewhere cannot revive it.
{
  const h = new History('aaa bbb ccc');
  const diag = h.bind({ range: { start: 4, end: 7 }, message: 'bbb' });
  h.commit([{ kind: 'delete', start: 4, end: 7 }]);
  h.commit([{ kind: 'insert', at: 0, text: 'bbb ' }]); // same text reappears
  console.log(`text: ${JSON.stringify(h.text())}`);
  show('invalid: deleted target is not revived by identical text', h.relocate(diag));
}

// 4. Ambiguous: identical context twice — candidates + tie evidence, no arbitrary pick.
{
  const h = new History('x foo y x foo y');
  h.commit([{ kind: 'insert', at: 0, text: 'Q' }]);
  const diag = h.bind({ range: { start: 3, end: 6 }, contextRadius: 2, message: 'foo' });
  h.undo(); // marker only exists on the undone branch -> text-similarity fallback
  show('ambiguous: duplicate fragment with identical context', h.relocate(diag));
}

// 5. Certain via text-similarity: duplicates with distinct context; loser excluded.
{
  const h = new History('foo bar foo');
  h.commit([{ kind: 'insert', at: 0, text: 'z' }]);
  const diag = h.bind({ range: { start: 1, end: 4 }, message: 'first foo' });
  h.undo();
  show('certain (text-similarity): distinct context, exclusion evidence', h.relocate(diag));
}

// 6. CRLF: line-ending-insensitive fallback, offsets still UTF-16 in the real text.
{
  const h = new History('line1\r\nline2\r\n');
  h.commit([{ kind: 'insert', at: 0, text: '!' }]);
  const diag = h.bind({ range: { start: 8, end: 13 }, message: 'line2' });
  h.undo();
  show('certain (text-similarity): CRLF document', h.relocate(diag));
}

// 7. Undo branches + history reclamation: identity degrades to similarity honestly.
{
  const h = new History('cat dog cat');
  h.commit([{ kind: 'insert', at: 0, text: '1' }]);
  const diag = h.bind({ range: { start: 5, end: 8 }, message: 'dog' });
  h.undo();
  h.commit([{ kind: 'insert', at: 0, text: '2' }]); // sibling branch
  console.log(`reclaimed versions: ${JSON.stringify(h.reclaim())}`);
  show('certain (text-similarity): bind version reclaimed', h.relocate(diag));
}
