import { merge3 } from './merge.ts';

function show(title: string, r: ReturnType<typeof merge3>) {
  console.log(`=== ${title} ===`);
  console.log(`eol: ${JSON.stringify(r.eol)}, clean: ${r.clean}`);
  console.log('--- merged ---');
  console.log(r.text.endsWith('\n') ? r.text : r.text + '\n');
  for (const c of r.conflicts) {
    console.log(`conflict @output[${c.start}, ${c.end}) base[${c.baseStart}, ${c.baseEnd})`);
  }
  console.log();
}

// 1. First line vs last line edits auto-merge (LF).
show('LF: first-line + last-line auto-merge', merge3(
  'alpha\nbeta\ngamma\n',
  'ALPHA\nbeta\ngamma\n',
  'alpha\nbeta\nGAMMA\n',
));

// 2. Different insertions at the same position -> structured conflict.
show('same-position different inserts -> conflict', merge3(
  'a\nb\n',
  'a\nX\nb\n',
  'a\nY\nb\n',
));

// 3. Identical change applied once.
show('identical change -> applied once', merge3(
  'one\ntwo\nthree\n',
  'one\nTWO\nthree\n',
  'one\nTWO\nthree\n',
));

// 4. CRLF + astral characters (UTF-16 offsets).
const crlfBase = '😀 first\r\nmiddle\r\nlast\r\n';
const crlfOurs = '😀 first\r\nMIDDLE-OURS\r\nlast\r\n';
const crlfTheirs = '😀 first\r\nMIDDLE-THEIRS\r\nlast\r\n';
const r = merge3(crlfBase, crlfOurs, crlfTheirs);
show('CRLF + emoji: overlapping edits conflict', r);
const c = r.conflicts[0];
console.log(`emoji line "😀 first" is ${'😀 first'.length} UTF-16 code units;`);
console.log(`base conflict offsets [${c.baseStart}, ${c.baseEnd}) -> ${JSON.stringify(crlfBase.slice(c.baseStart, c.baseEnd))}`);
