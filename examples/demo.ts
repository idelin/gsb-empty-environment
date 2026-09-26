/**
 * Runnable walkthrough.
 *   node --experimental-strip-types examples/demo.ts
 *
 * The fixture supplies the node tree, explicit per-cluster direction levels
 * and glyph widths; no browser/Unicode bidi engine performs the core math.
 */
import { BidiLayout, DocumentModel, VisualSelection } from "../src/index.ts";
import { fixedMetrics } from "../test/fixtures.ts";
import type { DocNode } from "../src/index.ts";

const line = (text: string): void => console.log(text);
const rule = (): void => console.log("\u2500".repeat(64));

const tree: DocNode = {
  id: "p",
  children: [
    {
      id: "t",
      text: "abcDEFghi",
      clusters: ["a", "b", "c", "D", "E", "F", "g", "h", "i"],
      levels:   [ 0,  0,  0,  1,  1,  1,  0,  0,  0],
    },
  ],
};

const model = new DocumentModel(tree);

line("1. Logical vs visual order");
rule();
const oneLine = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
console.log("logical:", "abcDEFghi", " levels:", "000111000");
console.log("visual :", oneLine.visualText(0));
console.log("boxes  :", oneLine.lines[0]!.items
  .map((item) => `${item.text}@x${item.x}${item.rtl ? "(rtl)" : ""}`).join(" "));

line("\n2. One logical offset, two visual boundaries (explicit affinity)");
rule();
for (const offset of [3, 6]) {
  const carets = oneLine.caretsAt(offset);
  console.log(
    `offset ${offset}:`,
    carets.map((c) => `x=${c.x} affinity=${c.affinity} line=${c.line}`).join(" | "),
  );
}
const hit = oneLine.hitTest(0, 30);
console.log(
  "click x=30 -> carets", hit.carets.map((c) => c.offset).join(" & "),
  "primary:", `${hit.primary.offset}/${hit.primary.affinity}`,
);

line("\n3. Visual Right arrow vs logical Forward");
rule();
let visual = oneLine.firstCaret();
const visualWalk: string[] = [];
for (let i = 0; i < 9; i++) {
  visual = oneLine.moveVisual(visual, "right");
  visualWalk.push(`${visual.offset}/${visual.affinity}`);
}
console.log("Right walk (offset/affinity):", visualWalk.join(" -> "));
let logical = oneLine.firstCaret();
const logicalWalk: number[] = [];
for (let i = 0; i < 6; i++) {
  logical = oneLine.moveLogical(logical, "forward");
  logicalWalk.push(logical.offset);
}
console.log("Forward walk (offsets)      :", logicalWalk.join(" -> "));

line("\n4. Soft wrap: dual positions across visual lines");
rule();
const wrapped = new BidiLayout(model, { maxWidth: 30, metrics: fixedMetrics(10) });
wrapped.lines.forEach((l, idx) => console.log(`line ${idx}: "${wrapped.visualText(idx)}"`));
const split = wrapped.caretsAt(3);
console.log("offset 3 carets:", split.map((c) => `line=${c.line} x=${c.x} ${c.affinity}`).join("  ;  "));
let caret = wrapped.moveVisual(wrapped.firstCaret(), "end");
console.log("End on line 0:", `${caret.offset}/${caret.affinity}@(line ${caret.line}, x ${caret.x})`);
caret = wrapped.moveVisual(caret, "right");
console.log("then Right   :", `${caret.offset}/${caret.affinity}@(line ${caret.line}, x ${caret.x})`);

line("\n5. Cross-visual-line selection copies back in logical order");
rule();
const anchor = wrapped.hitTest(0, 10).primary;
const active = wrapped.hitTest(2, 20).primary;
const selection = new VisualSelection(wrapped, anchor, active);
console.log("drag direction:", selection.visualDirection);
console.log("copied text  :", JSON.stringify(selection.copiedText()));
console.log("logical spans:", JSON.stringify(selection.logicalSpans()));
console.log("paint rects  :", selection.rects()
  .map((r) => `(line ${r.y / 16}, x ${r.x}-${r.x + r.width})`).join(" "));

line("\n6. Local level update -> new version, old hits rejected");
rule();
const updated = model.updateLevels("t", 3, 3, 0);
const relaidOut = new BidiLayout(updated, { maxWidth: 1000, metrics: fixedMetrics(10) });
console.log("after leveling DEF to 0, visual:", relaidOut.visualText(0), "version:", relaidOut.version);
try {
  relaidOut.assertCurrent(hit.primary);
} catch (error) {
  console.log("old hit rejected:", (error as Error).message);
}
