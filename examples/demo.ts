import { LayoutEngine, rowWidth } from '../src/index.ts';

const show = (label: string, value: unknown) =>
  console.log(label.padEnd(58), JSON.stringify(value));

// 1. Build an editable layout: tab stops, wide chars, combining marks,
//    soft wrap at column 12, LF + CRLF mixed.
const engine = new LayoutEngine('a\tb\n你好🌍 éx\r\nsoft wrap me please', {
  tabWidth: 4,
  wrapColumn: 12,
});

const render = (title: string) => {
  const snapshot = engine.snapshot();
  console.log(`\n=== ${title} (height=${snapshot.height} rows) ===`);
  for (const line of snapshot.lines) {
    line.rows.forEach((row, r) => {
      const cells = row.fragments
        .map((f) => {
          const text = engine.text.slice(line.start + f.start, line.start + f.start + f.length);
          return `${JSON.stringify(text)}@${f.col}+${f.width}`;
        })
        .join(' ');
      console.log(
        `  line ${line.index} row ${r} (w=${rowWidth(row)}${row.softBreakAfter ? ', wrap' : ''}): ${cells}`,
      );
    });
  }
  return snapshot;
};

const snap1 = render('initial layout');

// 2. Tab expansion: 'a' + tab reaches column 4 (tab width 4).
show('positionAt(offset of b)  // a\\tb', snap1.positionAt(2));

// 3. Wide char hit test: second column of 你, forward policy -> char start.
const line1 = snap1.lines[1];
show('hitTest(你 2nd col, forward)', snap1.hitTest(1, 0, 1, 'forward'));
show('hitTest(你 2nd col, backward)', snap1.hitTest(1, 0, 1, 'backward'));

// 4. Ambiguous soft-wrap boundary: one offset, two visual positions.
const wrapped = snap1.lines.find((l) => l.height > 1)!;
const lastOfRow0 = wrapped.rows[0].fragments.at(-1)!;
const wrapOffset = wrapped.start + lastOfRow0.start + lastOfRow0.length;
show('positionAt(wrap offset, upstream)', snap1.positionAt(wrapOffset, 'upstream'));
show('positionAt(wrap offset, downstream)', snap1.positionAt(wrapOffset, 'downstream'));

// 5. Partial measurement: edit only line 1, height changes, other
//    lines are NOT re-measured (rows shared by reference).
engine.applyEdit(line1.start, 0, 'inserted 你你');
console.log(`\n=== after editing line 1 only ===`);
console.log('re-measured lines:', engine.measuredLines);
const snap2 = render('after edit');
console.log('line 0 rows reused (not re-measured):', snap2.lines[0].rows === snap1.lines[0].rows);
console.log('line 2 rows reused (not re-measured):', snap2.lines[2].rows === snap1.lines[2].rows);

// 6. Old snapshot still answers hit tests against the old layout.
show('old snapshot hitTest(line1 row0 col1)', snap1.hitTest(1, 0, 1, 'forward'));
