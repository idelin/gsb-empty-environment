import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ChatListLayout } from "../src/layout.ts";
import { ChatViewport } from "../src/viewport.ts";

function makeViewport(count: number, itemHeight = 100, viewportHeight = 250) {
  const items = Array.from({ length: count }, (_, i) => ({ key: `m${i}`, height: itemHeight }));
  const layout = new ChatListLayout({ items });
  return new ChatViewport(layout, { viewportHeight });
}

describe("scroll anchoring", () => {
  it("keeps the first visible item and its inner offset when prepending", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350); // first visible: m3, 50px into it
    viewport.prepend([{ key: "h1", height: 100 }, { key: "h2", height: 100 }]);
    assert.equal(viewport.scrollOffset, 550);
    const location = viewport.layout.locate(viewport.scrollOffset)!;
    assert.equal(location.key, "m3");
    assert.equal(location.offsetInItem, 50);
  });

  it("accounts for estimated heights of unmeasured prepended items", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350);
    viewport.prepend([{ key: "h1" }, { key: "h2" }]); // default estimate 50
    assert.equal(viewport.scrollOffset, 450);
  });

  it("keeps the anchor stable when heights above it are corrected", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350);
    viewport.measure("m0", 180); // grows by 80 above the viewport
    assert.equal(viewport.scrollOffset, 430);
    viewport.measure("m1", 40); // shrinks by 60 above the viewport
    assert.equal(viewport.scrollOffset, 370);
  });

  it("keeps the scroll offset when the anchor item itself is re-measured", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350);
    viewport.measure("m3", 300); // anchor grows; its top does not move
    assert.equal(viewport.scrollOffset, 350);
  });

  it("ignores height corrections below the viewport", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350);
    viewport.measure("m8", 500);
    assert.equal(viewport.scrollOffset, 350);
  });

  it("falls back to the original successor when the anchor is removed", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350);
    viewport.removeKeys(["m3"]);
    assert.equal(viewport.scrollOffset, 300); // top of m4, the old successor
    assert.equal(viewport.layout.locate(viewport.scrollOffset)!.key, "m4");
  });

  it("falls back to the predecessor bottom when there is no successor", () => {
    const viewport = makeViewport(4, 100, 1000); // everything visible, no scroll range
    const tall = makeViewport(4, 100, 50);
    tall.scrollTo(400); // clamped to maxOffset = 350; anchor = m3 (last)
    assert.equal(tall.layout.locate(tall.scrollOffset)!.key, "m3");
    tall.removeKeys(["m3"]);
    assert.equal(tall.scrollOffset, 250); // bottom of m2, clamped to new maxOffset
    assert.equal(viewport.scrollOffset, 0);
  });

  it("falls back to the predecessor bottom when anchor and successor are removed", () => {
    const viewport = makeViewport(10);
    viewport.scrollTo(350);
    viewport.removeKeys(["m3", "m4"]);
    assert.equal(viewport.scrollOffset, 300); // bottom of m2
    assert.equal(viewport.layout.locate(viewport.scrollOffset)!.key, "m5");
  });

  it("resets to 0 when every item is removed", () => {
    const viewport = makeViewport(3);
    viewport.scrollTo(150);
    viewport.removeKeys(["m0", "m1", "m2"]);
    assert.equal(viewport.scrollOffset, 0);
    assert.equal(viewport.layout.count, 0);
  });

  it("clamps the resolved offset to the scrollable range", () => {
    const viewport = makeViewport(10, 100, 250);
    viewport.scrollTo(750); // maxOffset
    viewport.removeKeys(["m9"]); // content shrinks by 100
    assert.equal(viewport.scrollOffset, 650);
  });
});

describe("positioning by offset", () => {
  it("scrolls to an exact offset", () => {
    const viewport = makeViewport(10);
    assert.equal(viewport.scrollTo(420), 420);
    assert.equal(viewport.scrollOffset, 420);
  });

  it("clamps to the scrollable range at both boundaries", () => {
    const viewport = makeViewport(10, 100, 250);
    assert.equal(viewport.scrollTo(-50), 0);
    assert.equal(viewport.scrollTo(10_000), 750);
  });

  it("pins the offset to 0 while content fits the viewport", () => {
    const viewport = makeViewport(2, 100, 250);
    assert.equal(viewport.scrollTo(80), 0);
    viewport.append([{ key: "x", height: 100 }, { key: "y", height: 100 }]);
    assert.equal(viewport.scrollTo(200), 150); // maxOffset = 400 - 250
  });
});
