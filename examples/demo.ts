/**
 * Runnable sample: `npm run demo`
 * Shows tab expansion, wide/zero-width clusters, soft wrap, hit policies,
 * surrogate safety, incremental re-measurement and old-layout hit testing.
 */
import { GraphemeMeasurer, LayoutEngine } from '../src/index.ts';
import type { ClusterInfo, Measurer, VisualPosition } from '../src/index.ts';

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function showRows(engine: LayoutEngine): void {
  const text = engine.getText();
  for (const row of engine.allRows()) {
    const seg = JSON.stringify(text.slice(row.startOffset, row.endOffset));
    const cells = row.clusters
      .map((c) => `${JSON.stringify(text.slice(c.offset, c.offset + c.length))}@${c.column}w${c.width}`)
      .join(' ');
    console.log(
      `  L${row.line} R${row.row} cols 0..${row.width} [${row.startOffset}..${row.endOffset}] ${seg}\n    ${cells}`,
    );
  }
}

function hit(engine: LayoutEngine, pos: VisualPosition, policy: 'start' | 'end' = 'start'): void {
  const r = engine.visualToLogical(pos, policy);
  console.log(
    `  hit L${pos.line} R${pos.row} col ${pos.column} (${policy}) -> offset ${r.offset}, affinity ${r.affinity}`,
  );
}

// 1. Layout: tabs, wide chars, combining marks, emoji, CRLF, soft wrap.
show('layout: tab + wide + combining + emoji + CRLF, wrapColumn 10');
const engine = new LayoutEngine({
  text: 'a\tb世é😀xy\r\nsecond line here',
  tabWidth: 4,
  wrapColumn: 10,
});
showRows(engine);

// 2. Logical -> visual conversion.
show('logical -> visual');
for (const offset of [0, 1, 2, 3, 4, 5, 8]) {
  console.log(`  offset ${offset} ->`, engine.logicalToVisual(offset));
}

// 3. Hit testing with policies.
show('visual -> logical (hit policies)');
const wideCol = engine.logicalToVisual(engine.getText().indexOf('世')).column;
hit(engine, { line: 0, row: 0, column: 2 }); // inside the tab
hit(engine, { line: 0, row: 0, column: wideCol + 1 }, 'start'); // 2nd column of 世
hit(engine, { line: 0, row: 0, column: wideCol + 1 }, 'end');
hit(engine, { line: 0, row: 0, column: 10 }); // wrap boundary (row end)
hit(engine, { line: 0, row: 1, column: 0 }); // wrap boundary (next row)

// 4. Surrogate safety.
show('surrogate pair safety');
const emojiAt = engine.getText().indexOf('😀');
console.log(`  logicalToVisual(${emojiAt + 1}) [mid-pair] ->`, engine.logicalToVisual(emojiAt + 1));
try {
  engine.insert(emojiAt + 1, '!');
} catch (err) {
  console.log(`  insert(${emojiAt + 1}) rejected:`, (err as Error).message);
}

// 5. Incremental re-measurement.
show('incremental measure: edit one line, others untouched');
class CountingMeasurer implements Measurer {
  calls: string[] = [];
  private readonly inner = new GraphemeMeasurer();
  measure(line: string): ClusterInfo[] {
    this.calls.push(line);
    return this.inner.measure(line);
  }
}
const counter = new CountingMeasurer();
const inc = new LayoutEngine({ text: 'one\ntwo\nthree', wrapColumn: 4, measurer: counter });
[0, 1, 2].forEach((l) => inc.lineHeight(l));
console.log('  initial measure calls:', JSON.stringify(counter.calls));
counter.calls = [];
inc.insert(7, 'xyxyxy'); // line 1 grows 1 -> 3 rows
console.log('  line 1 height now:', inc.lineHeight(1));
inc.lineHeight(0);
inc.lineHeight(2);
console.log('  measure calls after edit:', JSON.stringify(counter.calls));

// 6. Old-layout hit testing via snapshot.
show('old-layout hit via snapshot');
const snap = inc.snapshot();
inc.delete(0, 4); // remove 'one\n'
console.log('  live text:', JSON.stringify(inc.getText()));
console.log('  live hit L0R0 col2 ->', inc.visualToLogical({ line: 0, row: 0, column: 2 }));
console.log('  snap hit L0R0 col2 ->', snap.visualToLogical({ line: 0, row: 0, column: 2 }), '(old layout)');
