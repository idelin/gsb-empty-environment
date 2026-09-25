import { describe, it } from "node:test";
import assert from "node:assert/strict";
import type { DateRule, EnumRule, WhitespaceRule } from "../src/types.ts";
import {
  WHITESPACE_PROBES,
  applyDate,
  applyEnum,
  applyWhitespace,
  dateDomain,
  enumDomain,
} from "../src/rules.ts";

const ws = (mode: "trim" | "collapse"): WhitespaceRule => ({ id: "ws", type: "whitespace", path: "/s", mode });
const date = (formats: string[], outputFormat: string, probeYears?: number[]): DateRule => ({
  id: "d", type: "date", path: "/s", formats, outputFormat, ...(probeYears ? { probeYears } : {}),
});
const en = (map: Record<string, string>, onUnknown: "error" | "keep" = "error"): EnumRule => ({
  id: "e", type: "enum", path: "/s", map, onUnknown,
});

describe("空白规则", () => {
  it("trim 只去首尾", () => {
    assert.equal(applyWhitespace(ws("trim"), "  a b  ").value, "a b");
    assert.equal(applyWhitespace(ws("trim"), "\t\na\n").value, "a");
  });
  it("collapse 折叠内部连续空白", () => {
    assert.equal(applyWhitespace(ws("collapse"), "  a   b\tc ").value, "a b c");
  });
  it("空白操作幂等（作用于自身输出不再变化）", () => {
    for (const mode of ["trim", "collapse"] as const) {
      for (const v of WHITESPACE_PROBES) {
        const once = applyWhitespace(ws(mode), v).value as string;
        assert.equal(applyWhitespace(ws(mode), once).value, once);
      }
    }
  });
});

describe("日期规则", () => {
  it("严格解析并按输出格式渲染", () => {
    assert.equal(applyDate(date(["YYYY-MM-DD"], "DD/MM/YYYY"), "2024-02-29").value, "29/02/2024");
  });
  it("拒绝无效日期（不回滚、不溢出）", () => {
    assert.equal(applyDate(date(["YYYY-MM-DD"], "YYYY-MM-DD"), "2021-02-29").ok, false);
    assert.equal(applyDate(date(["YYYY-MM-DD"], "YYYY-MM-DD"), "2021-13-01").ok, false);
    assert.equal(applyDate(date(["YYYY-MM-DD"], "YYYY-MM-DD"), "2021-00-10").ok, false);
  });
  it("无法解析不猜测：UNPARSEABLE", () => {
    const r = applyDate(date(["YYYY-MM-DD"], "YYYY-MM-DD"), "12/31/2021");
    assert.equal(r.ok, false);
    if (!r.ok) assert.equal(r.code, "DATE_UNPARSEABLE");
  });
  it("歧义日期（多格式解析为不同日期）拒绝：AMBIGUOUS", () => {
    const r = applyDate(date(["MM-DD-YYYY", "DD-MM-YYYY"], "YYYY-MM-DD"), "02-03-2021");
    assert.equal(r.ok, false);
    if (!r.ok) assert.equal(r.code, "DATE_AMBIGUOUS");
  });
  it("同一天被多格式接受不算歧义", () => {
    const r = applyDate(date(["YYYY-MM-DD", "YYYY/MM/DD"], "YYYY-MM-DD"), "2021/02/03");
    assert.equal(r.ok, true);
    if (r.ok) assert.equal(r.value, "2021-02-03");
  });
  it("声明有限域包含合法样例、输出形态与非法串", () => {
    const domain = dateDomain(date(["YYYY-MM-DD"], "YYYY-MM-DD", [2024]));
    assert.ok(domain.includes("2024-02-29"));
    assert.ok(domain.includes("2024-12-31"));
    assert.ok(domain.includes("not-a-date"));
    assert.ok(new Set(domain).size === domain.length);
  });
});

describe("枚举规则", () => {
  it("别名精确替换，不做大小写/空白宽容", () => {
    assert.equal(applyEnum(en({ M: "male" }), "M").value, "male");
    assert.equal(applyEnum(en({ M: "male" }), " M").ok, false);
    assert.equal(applyEnum(en({ M: "male" }), "m").ok, false);
  });
  it("未知值默认报错保留；keep 模式原样通过", () => {
    assert.equal(applyEnum(en({ M: "male" }), "Z").ok, false);
    const r = applyEnum(en({ M: "male" }, "keep"), "Z");
    assert.equal(r.ok, true);
    if (r.ok) assert.equal(r.value, "Z");
  });
  it("规范值本身命中即保持不变（幂等映射）", () => {
    const r = applyEnum(en({ male: "male", M: "male" }), "male");
    assert.equal(r.ok, true);
    if (r.ok) assert.equal(r.value, "male");
  });
  it("有限域 = 别名∪规范值∪未知", () => {
    assert.deepEqual(enumDomain(en({ M: "male", F: "female" })).sort(), ["F", "M", "__unknown__", "female", "male"]);
  });
});
