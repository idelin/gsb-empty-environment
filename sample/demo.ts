/**
 * End-to-end sample: multi-selection mapping across versions.
 * Run with: npm run sample
 */
import { History } from '../src/history.ts';
import {
  directionOf,
  mapSelection,
  mapSelectionBack,
  mapSelectionsForward,
  selectionFromUtf16,
  selectionToUtf16,
  type Selection,
} from '../src/selection.ts';

function show(h: History, sels: Selection[], label: string): void {
  const v = h.getVersion(h.head);
  console.log(`\n== ${label} ==`);
  console.log(`text (v${h.head}): ${JSON.stringify(h.textOf(h.head))}`);
  for (const s of sels) {
    const o = selectionToUtf16(v, s);
    console.log(
      `  sel#${s.id} anchor=${o.anchor} active=${o.active} direction=${directionOf(s)}` +
        (s.mergedFrom ? ` mergedFrom=[${s.mergedFrom.sources.map((x) => x.id).join(',')}]` : ''),
    );
  }
}

// 1. A document with a surrogate pair and both LF and CRLF line endings.
const h = new History('hello😀 world\r\nsecond\nthird');
show(h, [], 'initial document');

// 2. Three selections: forward, reverse, and a cursor.
const v1 = h.head;
const sels: Selection[] = [
  selectionFromUtf16(h.getVersion(v1), 1, 0, 5), // forward over "hello"
  selectionFromUtf16(h.getVersion(v1), 2, 17, 12), // reverse over "world"
  selectionFromUtf16(h.getVersion(v1), 3, 24, 24), // cursor in "second"
];
show(h, sels, 'three selections (forward / reverse / cursor)');

// 3. Insertion in front: everything shifts, the reverse selection stays reverse.
const v2 = h.addVersion({ start: 0, deleteCount: 0, insert: '>> ' });
let mapped = sels.map((s) => mapSelection(s, h.mappingBetween(v1, v2)));
show(h, mapped, 'after inserting ">> " at the start');

// 4. Two cursors pressed onto the same point by one deletion.
const h2 = new History('abcde');
const c1 = selectionFromUtf16(h2.getVersion(h2.head), 1, 1, 1);
const c2 = selectionFromUtf16(h2.getVersion(h2.head), 2, 3, 3);
const before = h2.head;
h2.addVersion({ start: 1, deleteCount: 2 }); // delete "bc"
const edge = h2.mappingBetween(before, h2.head);
const fwd = mapSelectionsForward([c1, c2], edge);
console.log('\n== two cursors, one deletion ("abcde" -> "ade") ==');
console.log(`  visible cursors: ${fwd.selections.length} (kept id=${fwd.selections[0].id})`);
console.log(`  merge report: ${JSON.stringify(fwd.merges)}`);
console.log(
  `  provenance: ${JSON.stringify(
    fwd.selections[0].mergedFrom?.sources.map((s) => ({
      id: s.id,
      direction: s.direction,
      anchorStick: s.anchor.stick,
      activeStick: s.active.stick,
    })),
  )}`,
);

// 5. Undo restores both cursors with their own identities.
h2.undo();
const restored = fwd.selections.flatMap((s) => mapSelectionBack(s, edge).selections);
show(h2, restored, 'after undo: both cursors restored');

// 6. Reverse mapping without provenance reports ambiguity and its source.
const h3 = new History('abcdef');
const a = h3.head;
const b = h3.addVersion({ start: 1, deleteCount: 4 }); // "af"
const cold = selectionFromUtf16(h3.getVersion(b), 9, 1, 1);
const back = mapSelectionBack(cold, h3.mappingBetween(a, b));
console.log('\n== ambiguity on reverse mapping of lost information ==');
console.log(`  ambiguous: ${back.ambiguous}`);
for (const msg of back.ambiguity) console.log(`  source: ${msg}`);

// 7. History compression keeps surviving-version queries intact.
const h4 = new History('compress me 😀\r\nplease');
const vids = [h4.head];
vids.push(h4.addVersion({ start: 0, deleteCount: 0, insert: '# ' }));
vids.push(h4.addVersion({ start: 9, deleteCount: 1 }));
vids.push(h4.addVersion({ start: 14, deleteCount: 2, insert: '\n' }));
h4.pin(vids[1]);
const before2 = h4.mappingBetween(vids[0], h4.head).forward.slice();
const reclaimed = h4.compress();
const after2 = h4.mappingBetween(vids[0], h4.head).forward.slice();
console.log('\n== history compression ==');
console.log(`  reclaimed versions: [${reclaimed.join(', ')}], surviving: [${h4.liveVersionIds.join(', ')}]`);
console.log(`  query v${vids[0]}->v${h4.head} unchanged: ${JSON.stringify(before2) === JSON.stringify(after2)}`);
