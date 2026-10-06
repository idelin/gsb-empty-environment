import { test } from "node:test";
import assert from "node:assert/strict";
import { sanitizeRaw, sanitizeWithCaret, isValidRaw } from "../src/sanitize.ts";

test("保留数字与合法中间态", () => {
  assert.equal(sanitizeRaw(""), "");
  assert.equal(sanitizeRaw("0"), "0");
  assert.equal(sanitizeRaw("-"), "-");
  assert.equal(sanitizeRaw("12."), "12.");
  assert.equal(sanitizeRaw(".5"), ".5");
  assert.equal(sanitizeRaw("-0.50"), "-0.50");
  assert.equal(sanitizeRaw("007"), "007");
});

test("剥离千位逗号与无关字符", () => {
  assert.equal(sanitizeRaw("1,234,567.89"), "1234567.89");
  assert.equal(sanitizeRaw("￥1,234元"), "1234");
  assert.equal(sanitizeRaw("abc"), "");
  assert.equal(sanitizeRaw("1 2 3"), "123");
});

test("负号仅能在首位", () => {
  assert.equal(sanitizeRaw("-5"), "-5");
  assert.equal(sanitizeRaw("--5"), "-5");
  assert.equal(sanitizeRaw("5-"), "5");
  assert.equal(sanitizeRaw("1-2-3"), "123");
});

test("小数点仅一个，先到先留", () => {
  assert.equal(sanitizeRaw("1.2.3"), "1.23");
  assert.equal(sanitizeRaw(".."), ".");
  assert.equal(sanitizeRaw("1..2"), "1.2");
});

test("sanitizeWithCaret 映射光标", () => {
  // 被丢弃字符在光标前：光标前移补偿
  assert.deepEqual(sanitizeWithCaret("12-3", 2), { raw: "123", caret: 2 });
  assert.deepEqual(sanitizeWithCaret("a1", 1), { raw: "1", caret: 0 });
  // 合法首字符负号保留
  assert.deepEqual(sanitizeWithCaret("-12", 1), { raw: "-12", caret: 1 });
  // 第二个小数点被丢弃，光标停在其前
  assert.deepEqual(sanitizeWithCaret("1.2.3", 4), { raw: "1.23", caret: 3 });
  // 光标在末尾
  assert.deepEqual(sanitizeWithCaret("12.", 3), { raw: "12.", caret: 3 });
});

test("isValidRaw", () => {
  for (const ok of ["", "-", "0", "12.", ".5", "-0.50", "007"]) {
    assert.ok(isValidRaw(ok), ok);
  }
  for (const bad of ["--1", "1-2", "1.2.3", "1,234", "a", "-.5."] ) {
    assert.ok(!isValidRaw(bad), bad);
  }
});
