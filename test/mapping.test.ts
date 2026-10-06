import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildPositionMap,
  rawIndexToDisplayIndex,
  displayIndexToRawIndex,
} from "../src/mapping.ts";

test("具体映射表：无逗号", () => {
  const map = buildPositionMap("12.3");
  assert.deepEqual(map.rawToDisplay, [0, 1, 2, 3, 4]);
  assert.deepEqual(map.displayToRaw, [0, 1, 2, 3, 4]);
});

test("具体映射表：含逗号", () => {
  const map = buildPositionMap("1234"); // "1,234"
  assert.deepEqual(map.rawToDisplay, [0, 1, 3, 4, 5]);
  assert.deepEqual(map.displayToRaw, [0, 1, 1, 2, 3, 4]);
});

test("具体映射表：负号 + 多逗号", () => {
  const map = buildPositionMap("-1234567"); // "-1,234,567"
  assert.deepEqual(map.rawToDisplay, [0, 1, 2, 4, 5, 6, 8, 9, 10]);
  assert.deepEqual(map.displayToRaw, [0, 1, 2, 2, 3, 4, 5, 5, 6, 7, 8]);
});

test("raw 边界可精确往返", () => {
  const raws = ["", "-", "1", "1234", "-1234567.89", "12.", ".5", "007", "123456789012.500"];
  for (const raw of raws) {
    const map = buildPositionMap(raw);
    for (let i = 0; i <= raw.length; i++) {
      assert.equal(map.displayToRaw[map.rawToDisplay[i]], i, `${raw} @${i}`);
    }
  }
});

test("逗号边界吸附到同一 raw 边界", () => {
  const raw = "1234"; // "1,234"
  assert.equal(displayIndexToRawIndex(raw, 1), 1); // 逗号前
  assert.equal(displayIndexToRawIndex(raw, 2), 1); // 逗号后
  assert.equal(rawIndexToDisplayIndex(raw, 1), 1);
});

test("越界下标被钳制", () => {
  assert.equal(rawIndexToDisplayIndex("12", 99), 2);
  assert.equal(displayIndexToRawIndex("12", -3), 0);
});
