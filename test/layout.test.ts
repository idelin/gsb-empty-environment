import { test } from "node:test";
import assert from "node:assert/strict";

import { layoutContainer } from "../src/layout.ts";
import { mixedGrid, crampedGrid } from "../src/fixtures.ts";
import type { LayoutContainer } from "../src/tree.ts";

test("item rects line up with resolved tracks and gaps", () => {
  const result = layoutContainer(mixedGrid);
  const [t0, t1, t2, t3] = result.trackSizes;
  const gap = mixedGrid.gap;

  const byId = new Map(result.items.map((item) => [item.id, item]));
  const logo = byId.get("logo")!;
  const nav = byId.get("nav")!;
  const hero = byId.get("hero")!;

  assert.equal(logo.x, 0);
  assert.equal(logo.width, t0);
  assert.equal(nav.x, t0 + gap);
  assert.equal(nav.width, t1);
  // hero spans columns 2..3: width includes the inner gap.
  assert.equal(hero.x, t0 + t1 + 2 * gap);
  assert.equal(hero.width, t2 + t3 + gap);
});

test("layout surfaces overflow diagnostics from track sizing", () => {
  const result = layoutContainer(crampedGrid);
  assert.equal(result.diagnostics.length, 1);
  assert.equal(result.diagnostics[0].kind, "container");
  for (const item of result.items) {
    assert.ok(item.width >= 0);
    assert.ok(Number.isFinite(item.x));
  }
});

test("invalid containers are rejected", () => {
  const bad: LayoutContainer = {
    width: -10,
    gap: 0,
    columns: [{ kind: "fixed", px: 10 }],
    items: [],
  };
  assert.throws(() => layoutContainer(bad), /width/);

  const badSpan: LayoutContainer = {
    width: 100,
    gap: 0,
    columns: [{ kind: "fixed", px: 10 }],
    items: [{ id: "oops", column: 0, columnSpan: 2, text: "a", fontSize: 16 }],
  };
  assert.throws(() => layoutContainer(badSpan), /span/);
});
