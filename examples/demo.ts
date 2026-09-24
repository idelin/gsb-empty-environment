/**
 * End-to-end demo: multi-selection mapping across versions, deletion merges,
 * ambiguity on reverse mapping, branch undo, and history compression.
 */
import { TextDocument } from '../src/document.ts';
import { Selection, SelectionSet } from '../src/selections.ts';
import { History } from '../src/history.ts';

const show = (label: string, set: SelectionSet) => {
  const parts = set.selections.map(
    (s) =>
      `#${s.id} ${s.direction === 'backward' ? '◀' : '▶'} anchor=${s.anchor.offset}(${s.anchor.affinity})` +
      ` active=${s.active.offset}(${s.active.affinity})` +
      (s.sources.length ? ` sources=[${s.sources.map((x) => `#${x.id}`).join(',')}]` : ''),
  );
  console.log(`  ${label}: ${parts.join('  |  ') || '(none)'}`);
};

console.log('=== 1. Two selections + a deletion that merges them ===');
const h = new History();
const v0 = h.init(
  TextDocument.fromText('aa111bb222cc'),
  SelectionSet.of(Selection.create(1, 5, 1), Selection.create(2, 7, 10)), // #1 backward, #2 forward
);
console.log(`  v0 text: ${JSON.stringify(h.docAt(v0).text())}`);
show('v0 selections', h.selectionsAt(v0));

const r1 = h.commit(4, 4, ''); // delete '1bb2'
console.log(`\n  edit: delete [${r1.edit.start},${r1.edit.end}) -> ${JSON.stringify(h.docAt(r1.version).text())}`);
show(`v${r1.version} selections`, h.selectionsAt(r1.version));
for (const m of r1.merges) {
  console.log(
    `  merge: into=#${m.into} range=[${m.from},${m.to}) lost=` +
      m.sources.map((s) => `#${s.id} ${s.direction} [${s.anchor.offset},${s.active.offset}]`).join('; '),
  );
}

console.log('\n=== 2. Reverse mapping: ambiguity for lost information ===');
const back = h.selectionsAt(r1.version).mapBackward(h.mappingBetween(v0, r1.version));
show('mapped back to v0', back.set);
for (const a of back.ambiguities) {
  if (a.kind === 'merged-sources') {
    console.log(
      `  ambiguity(merged-sources): selection #${a.selectionId} swallowed ` +
        a.lost.map((s) => `#${s.id} ${s.direction} anchor=${s.anchor.offset} active=${s.active.offset}`).join('; '),
    );
  } else {
    console.log(
      `  ambiguity(offset): selection #${a.selectionId} ${a.end} <- ` +
        a.sources.map((s) => `${s.kind}[${s.start},${s.end})`).join(', '),
    );
  }
}

console.log('\n=== 3. Branch undo restores the original selections ===');
const restored = h.checkout(v0);
show('after checkout(v0)', restored);

console.log('\n=== 4. Two cursors crushed by one deletion ===');
const h2 = new History();
const u0 = h2.init(
  TextDocument.fromText('aaXXbb'),
  SelectionSet.of(Selection.cursor(1, 2), Selection.cursor(2, 4)),
);
show('u0 cursors', h2.selectionsAt(u0));
const r2 = h2.commit(2, 2, ''); // delete 'XX'
console.log(`  edit: delete [2,4) -> ${JSON.stringify(h2.docAt(r2.version).text())}`);
show(`u${r2.version} cursors`, h2.selectionsAt(r2.version));
show('undo -> u0', h2.checkout(u0));

console.log('\n=== 5. History compression keeps live-version queries intact ===');
const h3 = new History();
const w0 = h3.init(
  TextDocument.fromText('hello 😀 world\r\nsecond line'),
  SelectionSet.of(Selection.create(1, 6, 8), Selection.cursor(2, 15)),
);
console.log(`  w0 text: ${JSON.stringify(h3.docAt(w0).text())} (len ${h3.docAt(w0).length} UTF-16 units)`);
const w1 = h3.commit(6, 2, '').version; // delete the emoji (2 UTF-16 units)
const w2 = h3.commit(0, 0, '>> ').version;
const w3 = h3.commit(15, 2, ' ').version; // CRLF -> space
console.log(`  w${w3} text: ${JSON.stringify(h3.docAt(w3).text())}`);
const before = h3.mappingBetween(w0, w3);
h3.reclaim(w1);
h3.reclaim(w2);
h3.compress();
const after = h3.mappingBetween(w0, w3);
console.log(`  versions kept after compress: ${h3.size} (reclaimed w${w1}, w${w2})`);
let identical = true;
for (let o = 0; o <= 30; o++) {
  for (const aff of ['before', 'after'] as const) {
    if (before.mapOffset(o, aff) !== after.mapOffset(o, aff)) identical = false;
  }
}
console.log(`  mapping w0->w${w3} identical before/after compression: ${identical}`);
show(`live selections at w${w3}`, h3.selectionsAt(w3));
