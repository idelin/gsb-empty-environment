import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { NormalizedText, codePointRuns, BIDI_CONTROL_CODEPOINTS } from "../src/unicode.ts";

describe("NormalizedText 长度：码点 / 字形簇 / UTF-16", () => {
  it("同一抽象字符串的预组合与组合重音：NFC 后三种长度相等", () => {
    const precomposed = new NormalizedText("café", "NFC");
    const decomposed = new NormalizedText("cafe\u0301", "NFC");

    assert.equal(precomposed.normalized, "café");
    assert.equal(decomposed.normalized, "café");

    for (const unit of ["codepoints", "graphemes", "utf16"] as const) {
      assert.equal(precomposed.length(unit), decomposed.length(unit));
      assert.equal(precomposed.length(unit), 4);
    }
  });

  it("NFD 把预组合字符拆成基本字符+组合记号，码点数增加但簇数不变", () => {
    const text = new NormalizedText("é", "NFD");
    assert.equal(text.normalized, "e\u0301");
    assert.equal(text.length("codepoints"), 2);
    assert.equal(text.length("graphemes"), 1);
    assert.equal(text.length("utf16"), 2);
  });

  it("emoji ZWJ 组合簇不能按码点数冒充簇数", () => {
    const family = "\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}";
    const text = new NormalizedText(family, "none");
    assert.equal(text.length("graphemes"), 1);
    assert.equal(text.length("codepoints"), 5);
    assert.equal(text.length("utf16"), 8);

    const waving = "\u{1F44B}\u{1F3FD}"; // 挥手 + 深肤色修饰
    const skin = new NormalizedText(waving, "none");
    assert.equal(skin.length("graphemes"), 1);
    assert.equal(skin.length("codepoints"), 2);
    assert.equal(skin.length("utf16"), 4);
  });

  it("代理对在 UTF-16 中占 2 个单元", () => {
    const text = new NormalizedText("a\u{1F600}b", "none");
    assert.equal(text.length("utf16"), 4);
    assert.equal(text.length("codepoints"), 3);
    assert.equal(text.length("graphemes"), 3);
  });
});

describe("规范化范围映射回原文", () => {
  it("组合重音规范化后位置变化，簇映射回原组合记号区间", () => {
    const original = "cafe\u0301";
    const text = new NormalizedText(original, "NFC");
    const clusters = text.graphemeRuns();
    assert.equal(clusters.length, 4);
    // 第 4 个簇在规范化串中只有一个预组合字符，但原文覆盖 e(索引3) 与 U+0301(索引4)
    assert.deepEqual(clusters[3], { start: 3, end: 5 });
    assert.equal(original.slice(clusters[3].start, clusters[3].end), "e\u0301");
  });

  it("多个原码点融合成一个字符时，映射覆盖全部参与者（不丢组合记号）", () => {
    const original = "o\u031B\u0309x"; // o + horn + hook above
    const text = new NormalizedText(original, "NFC");
    assert.equal(text.normalized, "ởx");
    const clusters = text.graphemeRuns();
    assert.deepEqual(clusters[0], { start: 0, end: 3 });
    assert.equal(original.slice(0, 3), "o\u031B\u0309");
    assert.deepEqual(clusters[1], { start: 3, end: 4 });
  });

  it("none 形式下映射即原码点区间", () => {
    const text = new NormalizedText("ab", "none");
    assert.deepEqual(text.graphemeRuns(), [{ start: 0, end: 1 }, { start: 1, end: 2 }]);
  });

  it("空字符串映射为零长度区间", () => {
    const text = new NormalizedText("", "NFC");
    assert.deepEqual(text.graphemeRuns(), []);
    assert.deepEqual(text.mapNormalizedRangeToOriginal({ start: 0, end: 0 }), { start: 0, end: 0 });
  });

  it("越界区间（码点单位）映射回包含组合记号的覆盖区间", () => {
    const text = new NormalizedText("abcde\u0301", "NFC");
    // 规范化后 5 个簇/码点（"abcde\u0301" -> "abcdé"），上限 4：越界为末簇
    const range = text.mapNormalizedRangeToOriginal({ start: 4, end: text.normalized.length });
    assert.equal(range.start, 4);
    assert.equal(range.end, 6);
  });
});

describe("双向控制字符定位", () => {
  it("在原文上逐个定位 Bidi 控制字符", () => {
    const text = new NormalizedText("a\u200Fb\u202Ec", "none");
    const hits = text.bidiControlsInOriginal();
    assert.equal(hits.length, 2);
    assert.deepEqual(hits.map((run) => run.codePoint), [0x200f, 0x202e]);
    assert.deepEqual(hits.map((run) => run.range), [{ start: 1, end: 2 }, { start: 3, end: 4 }]);
    assert.equal(BIDI_CONTROL_CODEPOINTS.length, 11);
  });

  it("codePointRuns 正确切分 BMP 与辅助平面字符", () => {
    const runs = codePointRuns("a\u{1F600}");
    assert.deepEqual(runs.map((run) => [run.codePoint, run.range.start, run.range.end]), [
      [0x61, 0, 1],
      [0x1f600, 1, 3],
    ]);
  });
});
