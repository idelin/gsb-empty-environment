import { createDiagnostic, relocate, type RelocationResult } from '../src/diagnostic.ts';
import { History } from '../src/history.ts';

function show(title: string, result: RelocationResult): void {
  console.log('\n=== ' + title + ' ===');
  if (result.kind === 'definite') {
    console.log(`definite (${result.via}) -> [${result.start}, ${result.end})`);
  } else if (result.kind === 'ambiguous') {
    console.log(`ambiguous -> ${result.candidates.length} candidates:`);
    for (const c of result.candidates) console.log(`  candidate [${c.start}, ${c.end}) ${c.match}`);
  } else {
    console.log(`invalid -> ${result.reason}`);
    for (const c of result.excluded) {
      console.log(`  excluded candidate [${c.start}, ${c.end}) ${c.match}: ${c.excluded}`);
    }
  }
  for (const step of result.evidence.steps) console.log('  evidence: ' + step);
}

{
  const h = new History('one\ntwo\nthree\n');
  const d = createDiagnostic(h, 4, 7, 'unused variable');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: 'zero\n' });
  show('1. insert before target shifts exactly (LF, UTF-16 offsets)', relocate(h, d));
}

{
  const h = new History('one\r\ntwo\r\nthree\r\n');
  const d = createDiagnostic(h, 5, 8, 'unused variable');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: 'zero\r\n' });
  show('2. same shift with CRLF line endings', relocate(h, d));
}

{
  const h = new History('foo bar');
  const d = createDiagnostic(h, 0, 3, 'bad call');
  h.applyEdit({ kind: 'delete', id: h.nextEditId(), start: 0, end: 4 });
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: h.text().length, text: ' foo' });
  show('3. deleted target does not revive when identical text appears elsewhere', relocate(h, d));
}

{
  const h = new History('p1\np2\np3\n');
  const d = createDiagnostic(h, 3, 5, 'inside p2');
  h.applyEdit({ kind: 'move', id: h.nextEditId(), start: 3, end: 6, to: 6, stableId: 'paragraph-p2' });
  show('4. move with stableId is followed precisely', relocate(h, d));
}

{
  const h = new History('p1\np2\np3\n');
  const d = createDiagnostic(h, 3, 5, 'inside p2');
  h.applyEdit({ kind: 'delete', id: h.nextEditId(), start: 3, end: 6 });
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: h.text().length, text: 'p2\n' });
  show('5. cut-paste without stableId is NOT followed', relocate(h, d));
}

{
  const unit = 'foo-bar-';
  const h = new History(unit.repeat(10));
  const d = createDiagnostic(h, 5 * unit.length, 5 * unit.length + 3, 'on foo');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: '//' });
  h.reclaim(0);
  show('6. duplicate context after history reclaim -> ambiguous candidates', relocate(h, d));
}

{
  const h = new History('hello world');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: 'say ' });
  const d = createDiagnostic(h, 10, 15, 'on world');
  h.undo();
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 5, text: '!' });
  show('7. undo branch: identity follows across undo + new edit', relocate(h, d));
}

{
  const h = new History('alpha beta gamma');
  const d = createDiagnostic(h, 6, 10, 'on beta');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: '1 ' });
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: '2 ' });
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: '3 ' });
  const gc = h.reclaim(1, new Set([d.version]));
  const r = relocate(h, d);
  show(`8. history reclaim squashes edits into checkpoint v${gc.checkpoint}, identity preserved`, r);
}

{
  const h = new History('a\u{1F600}b');
  try {
    createDiagnostic(h, 1, 2);
  } catch (e) {
    console.log('\n=== 9. surrogate pair protection ===');
    console.log('  rejected diagnostic [1, 2): ' + (e as Error).message);
  }
  const d = createDiagnostic(h, 1, 3, 'emoji spans 2 UTF-16 units');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: 'xy' });
  show('9b. emoji range shifts by UTF-16 units', relocate(h, d));
}
