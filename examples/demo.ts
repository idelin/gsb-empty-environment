/**
 * Runnable walkthrough (no test framework):
 *
 *   node examples/demo.ts
 *
 * It prints, for a fixture paragraph with pre-assigned levels:
 *   1. logical vs. visual order, including an embedded even-level island;
 *   2. the two visual homes of one logical offset at a direction boundary;
 *   3. physical visual-right caret traversal (the bidi "zig-zag");
 *   4. a soft wrap in an rtl paragraph with explicit upper/lower affinity;
 *   5. cross-visual-line selection copied back in logical order;
 *   6. a local level update bumping the layout version.
 */

import { BidiTextModel } from '../src/model.ts';
import { StaleLayoutError } from '../src/types.ts';
import { fixedMetrics, nestedTree, rtlTree } from '../src/fixtures.ts';

const ruler = (label: string): void => console.log(`\n=== ${label} ===`);

ruler('1. logical order vs. visual order');
{
  const model = new BidiTextModel(nestedTree(), fixedMetrics(), 100);
  const line = model.lines[0]!;
  console.log('logical text : ab AB XY c');
  console.log('levels       :  0  1  2  0   (even=ltr, odd=rtl)');
  console.log('visual boxes :', line.boxes.map((b) => b.text).join(' '));
  console.log('logical idx  :', line.boxes.map((b) => b.clusterIndex).join(' '));
  console.log('x positions  :', line.boxes.map((b) => b.x).join(' '));
}

ruler('2. one logical offset -> two visual boundaries (offset 2, run entry)');
{
  const model = new BidiTextModel(nestedTree(), fixedMetrics(), 100);
  for (const caret of model.caretsAtOffset(2)) {
    console.log(
      `  line=${caret.line} x=${caret.x} edge=${caret.affinity.edge} level=${caret.affinity.level} bias=${caret.lineBias}`,
    );
  }
  console.log('hit test at x=2 returns offsets:', model.hitTest(0, 2).map((c) => c.offset).sort((a, b) => a - b));
}

ruler('3. physical "right" arrow traverses the bidi zig-zag');
{
  const model = new BidiTextModel(nestedTree(), fixedMetrics(), 100);
  let caret = model.primaryCaret(0);
  const path: string[] = [`off${caret.offset}@x${caret.x}`];
  for (;;) {
    const next = model.moveVisual(caret, 'right');
    if (!next) break;
    caret = next;
    path.push(`off${caret.offset}@x${caret.x}`);
  }
  console.log(path.join('  ->  '));
}

ruler('4. soft wrap in a pure rtl paragraph (width = 3)');
{
  const model = new BidiTextModel(rtlTree(), fixedMetrics(), 3);
  model.lines.forEach((line) => {
    console.log(`line ${line.index}: visual = ${line.boxes.map((b) => b.text).join(' ')}`);
  });
  const homes = model.caretsAtOffset(3);
  console.log('wrap offset 3 homes:');
  homes.forEach((c) =>
    console.log(`  bias=${c.lineBias} line=${c.line} x=${c.x} edge=${c.affinity.edge}`),
  );
  const upper = homes.find((c) => c.lineBias === 'upper')!;
  const across = model.moveVisual(upper, 'left')!;
  console.log(`physical LEFT off the upper home lands on: line=${across.line} bias=${across.lineBias} x=${across.x}`);
}

ruler('5. cross-visual-line selection, copied back in LOGICAL order');
{
  const model = new BidiTextModel(rtlTree(), fixedMetrics(), 3);
  const start = model.layout.carets.find((c) => c.line === 0 && c.x === 3 && c.offset === 0)!;
  const end = model.layout.carets.find((c) => c.line === 1 && c.x === 0 && c.offset === 5)!;
  const selection = model.select(start, end);
  console.log('selected visual lines:', selection.visualRanges().map((r) => r.line).join(', '));
  console.log('range geometry:', selection.visualRanges().map((r) => `line${r.line}[${r.x1}..${r.x2}]`).join(' '));
  console.log('copy (logical order) :', JSON.stringify(model.copySelection(selection)));
}

ruler('6. local level update -> new layout version, old caret rejected');
{
  const model = new BidiTextModel(nestedTree(), fixedMetrics(), 100);
  const oldCaret = model.primaryCaret(2);
  console.log(`before: version=${oldCaret.version}, visual=${model.lines[0]!.boxes.map((b) => b.text).join(' ')}`);
  model.setNodeLevel('t-rtl', 0); // the rtl AB turns ltr
  console.log(`after : version=${model.version}, visual=${model.lines[0]!.boxes.map((b) => b.text).join(' ')}`);
  try {
    model.moveVisual(oldCaret, 'right');
  } catch (error) {
    if (error instanceof StaleLayoutError) console.log(`old caret rejected: ${error.message}`);
  }
}
