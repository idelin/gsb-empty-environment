import { test } from "node:test";
import assert from "node:assert/strict";
import { MarkerSet, type Marker } from "../src/markers.ts";
import { modelSegments } from "./model.ts";

const TEXT_LENGTH = 64;

function marker(partial: Partial<Marker> & { id: string }): Marker {
  return {
    start: 0,
    end: 0,
    priority: 0,
    sourceId: "a",
    attrs: {},
    ...partial,
  };
}

function assertMatchesModel(set: MarkerSet, length = TEXT_LENGTH): void {
  assert.deepEqual(set.segments(0, length), modelSegments(set.markers(), 0, length));
}

test("half-open ranges: end is exclusive, touching ranges do not overlap", () => {
  const set = new MarkerSet([
    marker({ id: "m1", start: 0, end: 4, attrs: { color: "red" } }),
    marker({ id: "m2", start: 4, end: 8, attrs: { color: "blue" } }),
  ]);
  assert.deepEqual(set.segments(), [
    { start: 0, end: 4, attrs: { color: "red" } },
    { start: 4, end: 8, attrs: { color: "blue" } },
  ]);
  assert.deepEqual(set.resolveAt(3), { color: "red" });
  assert.deepEqual(set.resolveAt(4), { color: "blue" });
  assertMatchesModel(set);
});

test("higher priority wins on overlap, per attribute key", () => {
  const set = new MarkerSet([
    marker({ id: "low", start: 0, end: 10, priority: 1, attrs: { color: "red", bold: true } }),
    marker({ id: "high", start: 4, end: 6, priority: 5, attrs: { color: "green" } }),
  ]);
  assert.deepEqual(set.segments(), [
    { start: 0, end: 4, attrs: { color: "red", bold: true } },
    { start: 4, end: 6, attrs: { color: "green", bold: true } },
    { start: 6, end: 10, attrs: { color: "red", bold: true } },
  ]);
  assertMatchesModel(set);
});

test("same priority is arbitrated by sourceId (larger wins), then marker id", () => {
  const bySource = new MarkerSet([
    marker({ id: "m1", start: 0, end: 5, priority: 2, sourceId: "lint", attrs: { color: "red" } }),
    marker({ id: "m2", start: 0, end: 5, priority: 2, sourceId: "syntax", attrs: { color: "blue" } }),
  ]);
  assert.deepEqual(bySource.segments(), [{ start: 0, end: 5, attrs: { color: "blue" } }]);

  const byId = new MarkerSet([
    marker({ id: "aaa", start: 0, end: 5, priority: 2, sourceId: "lint", attrs: { color: "red" } }),
    marker({ id: "bbb", start: 0, end: 5, priority: 2, sourceId: "lint", attrs: { color: "blue" } }),
  ]);
  assert.deepEqual(byId.segments(), [{ start: 0, end: 5, attrs: { color: "red" } }]);
  assertMatchesModel(bySource);
  assertMatchesModel(byId);
});

test("attributes from different markers merge key-by-key", () => {
  const set = new MarkerSet([
    marker({ id: "m1", start: 0, end: 8, priority: 1, attrs: { color: "red" } }),
    marker({ id: "m2", start: 4, end: 12, priority: 1, attrs: { underline: true } }),
  ]);
  assert.deepEqual(set.segments(), [
    { start: 0, end: 4, attrs: { color: "red" } },
    { start: 4, end: 8, attrs: { color: "red", underline: true } },
    { start: 8, end: 12, attrs: { underline: true } },
  ]);
  assertMatchesModel(set);
});

test("empty ranges are anchors: they occupy no characters and emit no segments", () => {
  const set = new MarkerSet([
    marker({ id: "anchor1", start: 3, end: 3, priority: 99, attrs: { cursor: true } }),
    marker({ id: "anchor2", start: 7, end: 7, attrs: { bookmark: "x" } }),
    marker({ id: "fill", start: 0, end: 10, attrs: { color: "gray" } }),
  ]);
  assert.deepEqual(set.segments(), [{ start: 0, end: 10, attrs: { color: "gray" } }]);
  assert.deepEqual(set.resolveAt(3), { color: "gray" });
  assert.deepEqual(set.anchors().map((m) => m.id), ["anchor1", "anchor2"]);
  assert.deepEqual(set.anchors(4, 10).map((m) => m.id), ["anchor2"]);

  const anchorsOnly = new MarkerSet([marker({ id: "a", start: 2, end: 2, attrs: { x: 1 } })]);
  assert.deepEqual(anchorsOnly.segments(), []);
  assertMatchesModel(set);
  assertMatchesModel(anchorsOnly);
});

test("update re-resolves range, priority and attrs", () => {
  const set = new MarkerSet([
    marker({ id: "m1", start: 0, end: 6, priority: 1, attrs: { color: "red" } }),
    marker({ id: "m2", start: 3, end: 9, priority: 1, attrs: { color: "blue" } }),
  ]);
  assert.deepEqual(set.segments(), [
    { start: 0, end: 6, attrs: { color: "red" } },
    { start: 6, end: 9, attrs: { color: "blue" } },
  ]);

  set.update("m2", { priority: 5 });
  assert.deepEqual(set.segments(), [
    { start: 0, end: 3, attrs: { color: "red" } },
    { start: 3, end: 9, attrs: { color: "blue" } },
  ]);

  set.update("m2", { start: 6, end: 8 });
  assert.deepEqual(set.segments(), [
    { start: 0, end: 6, attrs: { color: "red" } },
    { start: 6, end: 8, attrs: { color: "blue" } },
  ]);

  set.update("m1", { attrs: { color: "blue" } });
  assert.deepEqual(set.segments(), [{ start: 0, end: 8, attrs: { color: "blue" } }]);

  assert.throws(() => set.update("m1", { start: 5, end: 2 }), RangeError);
  assert.throws(() => set.update("nope", { priority: 1 }), /unknown marker id/);
  assertMatchesModel(set);
});

test("remove deletes markers and re-resolves segments", () => {
  const set = new MarkerSet([
    marker({ id: "base", start: 0, end: 10, attrs: { color: "gray" } }),
    marker({ id: "top", start: 2, end: 8, priority: 3, attrs: { color: "red" } }),
  ]);
  assert.equal(set.remove("top"), true);
  assert.equal(set.remove("top"), false);
  assert.deepEqual(set.segments(), [{ start: 0, end: 10, attrs: { color: "gray" } }]);
  assert.equal(set.remove("base"), true);
  assert.deepEqual(set.segments(), []);
  assertMatchesModel(set);
});

test("adjacent segments with identical final styles merge, others stay split", () => {
  const set = new MarkerSet([
    marker({ id: "m1", start: 0, end: 4, attrs: { color: "red" } }),
    marker({ id: "m2", start: 4, end: 9, attrs: { color: "red" } }),
    marker({ id: "m3", start: 9, end: 12, attrs: { color: "red", bold: true } }),
    marker({ id: "m4", start: 12, end: 15, attrs: { bold: true } }),
  ]);
  assert.deepEqual(set.segments(), [
    { start: 0, end: 9, attrs: { color: "red" } },
    { start: 9, end: 12, attrs: { color: "red", bold: true } },
    { start: 12, end: 15, attrs: { bold: true } },
  ]);

  // An update that equalizes styles must merge previously split segments.
  set.update("m3", { attrs: { color: "red" } });
  assert.deepEqual(set.segments(), [
    { start: 0, end: 12, attrs: { color: "red" } },
    { start: 12, end: 15, attrs: { bold: true } },
  ]);

  // Gaps are omitted, not merged across.
  const gapped = new MarkerSet([
    marker({ id: "g1", start: 0, end: 2, attrs: { color: "red" } }),
    marker({ id: "g2", start: 5, end: 7, attrs: { color: "red" } }),
  ]);
  assert.deepEqual(gapped.segments(), [
    { start: 0, end: 2, attrs: { color: "red" } },
    { start: 5, end: 7, attrs: { color: "red" } },
  ]);
  assertMatchesModel(set);
  assertMatchesModel(gapped);
});

test("segments(from, to) clips to the queried window", () => {
  const set = new MarkerSet([
    marker({ id: "m1", start: 0, end: 10, attrs: { color: "red" } }),
    marker({ id: "m2", start: 8, end: 20, priority: 1, attrs: { color: "blue" } }),
  ]);
  assert.deepEqual(set.segments(4, 12), [
    { start: 4, end: 8, attrs: { color: "red" } },
    { start: 8, end: 12, attrs: { color: "blue" } },
  ]);
  assertMatchesModel(set);
});

test("invalid ranges and duplicate ids are rejected", () => {
  assert.throws(() => new MarkerSet([marker({ id: "bad", start: 3, end: 3.5 })]), RangeError);
  assert.throws(() => new MarkerSet([marker({ id: "bad", start: 5, end: 2 })]), RangeError);
  assert.throws(() => new MarkerSet([marker({ id: "bad", start: -1, end: 2 })]), RangeError);
  const set = new MarkerSet([marker({ id: "m", start: 0, end: 1 })]);
  assert.throws(() => set.add(marker({ id: "m", start: 0, end: 1 })), /duplicate marker id/);
});

test("randomized cross-check against the per-character model", () => {
  let seed = 0x2f6e2b1;
  const random = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  const pick = <T>(items: readonly T[]): T => items[Math.floor(random() * items.length)]!;
  const sources = ["syntax", "lint", "spell", "git"];
  const keys = ["color", "bold", "underline", "italic"];
  const values = ["red", "blue", "green", true, false, 1, 2] as const;

  for (let round = 0; round < 200; round++) {
    const set = new MarkerSet();
    const ids: string[] = [];
    const addRandom = (id: string): void => {
      const start = Math.floor(random() * TEXT_LENGTH);
      const isAnchor = random() < 0.2;
      const end = isAnchor
        ? start
        : Math.min(TEXT_LENGTH, start + 1 + Math.floor(random() * 12));
      const attrs: Marker["attrs"] = {};
      for (const key of keys) {
        if (random() < 0.4) attrs[key] = pick(values);
      }
      set.add(
        marker({
          id,
          start,
          end,
          priority: Math.floor(random() * 4),
          sourceId: pick(sources),
          attrs,
        }),
      );
      ids.push(id);
    };

    for (let i = 0; i < 12; i++) addRandom(`m${i}`);
    assertMatchesModel(set);

    for (let step = 0; step < 8; step++) {
      const id = pick(ids);
      const action = random();
      if (action < 0.4 && set.has(id)) {
        set.remove(id);
      } else if (action < 0.8 && set.has(id)) {
        const start = Math.floor(random() * TEXT_LENGTH);
        const end = random() < 0.2 ? start : Math.min(TEXT_LENGTH, start + Math.floor(random() * 10));
        set.update(id, {
          start,
          end,
          priority: Math.floor(random() * 4),
          attrs: { [pick(keys)]: pick(values) },
        });
      } else {
        addRandom(`m${round}-${step}`);
      }
      assertMatchesModel(set);
    }
  }
});
