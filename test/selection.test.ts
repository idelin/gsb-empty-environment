import { test } from "node:test";
import assert from "node:assert/strict";
import { DocumentModel } from "../src/model.ts";
import { BidiLayout } from "../src/layout.ts";
import { VisualSelection } from "../src/selection.ts";
import { fixedMetrics, mixedTree } from "./fixtures.ts";

test("visual selection copies back in LOGICAL order", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  // visual row: a b c F E D g h i ; select F E D (x 30..60)
  const anchor = layout.caretAt(6, "backward"); // x=30
  const active = layout.caretAt(3, "forward"); // x=60
  const selection = new VisualSelection(layout, anchor, active);
  assert.equal(selection.visualDirection, "rightward");
  // copied text is logical D E F, not visual F E D
  assert.equal(selection.copiedText(), "DEF");
  assert.deepEqual(selection.logicalSpans(), [{ from: 3, to: 6 }]);
});

test("reversed drag keeps its direction and yields the same copied content", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const a = layout.caretAt(3, "forward"); // x=60
  const b = layout.caretAt(6, "backward"); // x=30
  const selection = new VisualSelection(layout, a, b);
  assert.equal(selection.visualDirection, "leftward");
  assert.equal(selection.copiedText(), "DEF");
});

test("a visual bidi selection may be logically non-contiguous", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  // select visual c F E (slots x=20..50): logical {c=2} + {D=4,E=5}
  const anchor = layout.hitTest(0, 20).primary;
  const active = layout.hitTest(0, 50).primary;
  const selection = new VisualSelection(layout, anchor, active);
  assert.equal(selection.copiedText(), "cEF");
  assert.deepEqual(selection.logicalSpans(), [
    { from: 2, to: 3 },
    { from: 4, to: 6 },
  ]);
});

test("selection spans visual lines after soft wrap", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 30, metrics: fixedMetrics(10) });
  // lines: abc | FED | ghi
  const anchor = layout.hitTest(0, 10).primary; // line0
  const active = layout.hitTest(2, 20).primary; // line2
  const selection = new VisualSelection(layout, anchor, active);
  assert.deepEqual(
    selection.rects().map((r) => `${r.y / 16}:${r.x}-${r.x + r.width}`),
    ["0:10-30", "1:0-30", "2:0-20"],
  );
  assert.equal(selection.copiedText(), "bcDEFgh");
});

test("empty selection copies nothing", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const caret = layout.caretAt(4, "forward");
  const selection = new VisualSelection(layout, caret, caret);
  assert.equal(selection.isEmpty, true);
  assert.equal(selection.copiedText(), "");
  assert.deepEqual(selection.rects(), []);
});

test("selection refuses carets from a newer layout version", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const model2 = model.updateLevels("t", 3, 3, 0);
  const layout2 = new BidiLayout(model2, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const oldCaret = layout.caretAt(3, "forward");
  const newCaret = layout2.caretAt(3, "forward");
  assert.throws(
    () => new VisualSelection(layout2, oldCaret, newCaret),
    /Stale selection anchor/,
  );
  const selection = new VisualSelection(layout, layout.firstCaret(), layout.caretAt(2, "backward"));
  // using it against the old layout still works
  assert.equal(selection.copiedText(), "ab");
  // mixing the old selection with a layout built after another local update
  // is rejected at the first query
  const another = new BidiLayout(model.updateLevels("t", 0, 1, 1), {
    maxWidth: 1000,
    metrics: fixedMetrics(10),
  });
  const mixed = new VisualSelection(
    layout,
    layout.firstCaret(),
    oldCaret,
  ) as unknown as { layout: BidiLayout };
  (mixed as { layout: BidiLayout }).layout = another;
  assert.throws(() => (mixed as unknown as VisualSelection).copiedText(), /Stale selection/);
});
