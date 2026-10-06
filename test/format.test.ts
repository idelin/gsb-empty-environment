import { test } from "node:test";
import assert from "node:assert/strict";
import { formatDisplay } from "../src/format.ts";
import { sanitizeRaw } from "../src/sanitize.ts";

test("整数部分插入千位分隔符", () => {
  assert.equal(formatDisplay(""), "");
  assert.equal(formatDisplay("0"), "0");
  assert.equal(formatDisplay("123"), "123");
  assert.equal(formatDisplay("1234"), "1,234");
  assert.equal(formatDisplay("1234567"), "1,234,567");
  assert.equal(formatDisplay("123456789012"), "123,456,789,012");
});

test("符号与小数部分原样保留", () => {
  assert.equal(formatDisplay("-"), "-");
  assert.equal(formatDisplay("-1234.56"), "-1,234.56");
  assert.equal(formatDisplay("12."), "12.");
  assert.equal(formatDisplay("1234."), "1,234.");
  assert.equal(formatDisplay(".5"), ".5");
  assert.equal(formatDisplay("0.50"), "0.50");
  assert.equal(formatDisplay("007"), "007");
  // 小数部分不分组
  assert.equal(formatDisplay("123.4567"), "123.4567");
});

test("display 可被 sanitizeRaw 还原为 raw", () => {
  for (const raw of ["", "-", "1", "1234", "-1234567.89", "12.", ".5", "007"]) {
    assert.equal(sanitizeRaw(formatDisplay(raw)), raw, raw);
  }
});
