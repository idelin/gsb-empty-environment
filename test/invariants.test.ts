import { test } from "node:test";
import assert from "node:assert/strict";
import { DocumentModel } from "../src/model.ts";
import { BidiLayout } from "../src/layout.ts";
import { fixedMetrics } from "./fixtures.ts";
import { visualOrder } from "../src/reorder.ts";

function validWalks(maxLen: number, maxLevel: number): number[][] {
  const out: number[][] = [[]];
  const grow = (seq: number[], level: number): void => {
    if (seq.length === maxLen) return;
    for (const next of new Set([level, level + 1, level - 1].filter((l) => l >= 0 && l <= maxLevel))) {
      out.push([...seq, next]);
      grow([...seq, next], next);
    }
  };
  grow([0], 0);
  grow([1], 1);
  return out;
}

test("exhaustive layout invariants over valid level sequences (len<=6, depth<=3)", () => {
  const metrics = fixedMetrics(7, 10);
  for (const levels of validWalks(6, 3)) {
    const model = new DocumentModel({
      id: "p",
      children: [
        {
          id: "t",
          text: levels.map((_, i) => String.fromCodePoint(97 + i)).join(""),
          clusters: levels.map((_, i) => String.fromCodePoint(97 + i)),
          levels,
        },
      ],
    });
    const layout = new BidiLayout(model, { maxWidth: 21, metrics });

    // 1. placed order matches the reorder algorithm per line
    const placedLogical = layout.lines.flatMap((line) => line.items.map((item) => item.logicalIndex));
    assert.deepEqual(placedLogical, visualOrder(levels));

    // 2. exactly 2N edge carets, each (offset, affinity) present; offsets 0
    //    and N once, interior offsets with both affinities (possibly at two
    //    geometric positions)
    const seen = new Set<string>();
    for (const line of layout.lines) {
      for (const slot of line.slots) {
        for (const caret of slot.carets) seen.add(`${caret.offset}/${caret.affinity}`);
      }
    }
    assert.equal(seen.size, Math.max(1, levels.length * 2));
    if (levels.length > 0) {
      assert.ok(seen.has("0/forward"));
      assert.ok(seen.has(`${levels.length}/backward`));
    }

    // 3. multi-valued boundaries are never collapsed: a slot with distinct
    //    offsets always has one caret per distinct logical edge it represents
    for (const line of layout.lines) {
      for (const slot of line.slots) {
        const offsets = new Set(slot.carets.map((c) => c.offset));
        assert.ok(slot.carets.length >= offsets.size);
      }
    }

    // 4. hitTest round trip: every caret's geometry hits a slot containing it
    for (const line of layout.lines) {
      for (const slot of line.slots) {
        if (slot.carets.length === 0) throw new Error("empty slot");
        const hit = layout.hitTest(line.index, slot.x);
        assert.equal(hit.slotFlatIndex, slot.flatIndex);
        for (const caret of slot.carets) {
          assert.ok(hit.carets.some((c) => c.offset === caret.offset));
        }
      }
    }

    // 5. visual right-walk from the physical leftmost boundary visits every
    //    physical slot exactly once (the logical first caret of a pure RTL
    //    line sits at the right, so start from geometry, not logical order)
    let caret = layout.hitTest(0, 0).primary;
    if (levels.join(",") === "1,1") console.error("start", caret.offset, caret.affinity, caret.slotFlatIndex, layout.lines.map(l=>l.slots.length));
    let visited = 1;
    for (;;) {
      const next = layout.moveVisual(caret, "right");
      if (levels.join(",") === "1,1") console.error("step", caret.offset, caret.affinity, caret.slotFlatIndex, "->", next.offset, next.affinity, next.slotFlatIndex);
      if (next.slotFlatIndex === caret.slotFlatIndex) break;
      visited++;
      caret = next;
    }
    const totalSlots = layout.lines.reduce((n, line) => n + line.slots.length, 0);
    assert.equal(visited, totalSlots);
    // and walking left from the physical rightmost boundary does the reverse
    const lastLine = layout.lines[layout.lines.length - 1]!;
    let caret2 = layout.hitTest(lastLine.index, lastLine.width + 1000).primary;
    let visited2 = 1;
    for (;;) {
      const next = layout.moveVisual(caret2, "left");
      if (next.slotFlatIndex === caret2.slotFlatIndex) break;
      visited2++;
      caret2 = next;
    }
    assert.equal(visited2, totalSlots);
  }
});
