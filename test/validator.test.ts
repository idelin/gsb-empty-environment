import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { Validator, SchemaError } from "../src/validator.ts";
import type { Json } from "../src/schema.ts";

describe("长度校验：三种单位独立生效", () => {
  it("codepoints / graphemes / utf16 分别按各自单位计数", () => {
    const family = "\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}";

    const graphemeValidator = new Validator({ type: "string", maxLength: 0, maxLengthUnit: "graphemes" });
    assert.equal(graphemeValidator.validate(family).valid, false);

    const graphemeOk = new Validator({ type: "string", maxLength: 1, maxLengthUnit: "graphemes" });
    assert.equal(graphemeOk.validate(family).valid, true);

    const cpValidator = new Validator({ type: "string", maxLength: 4, maxLengthUnit: "codepoints" });
    const cpResult = cpValidator.validate(family);
    assert.equal(cpResult.valid, false);
    assert.equal(cpResult.errors[0].message.includes("5 codepoints 超过上限 4"), true);

    const u16Validator = new Validator({ type: "string", maxLength: 7, maxLengthUnit: "utf16" });
    const u16Result = u16Validator.validate(family);
    assert.equal(u16Result.valid, false);
    assert.match(u16Result.errors[0].message, /8 utf16 超过上限 7/);
  });

  it("minLength 默认按码点计", () => {
    const validator = new Validator({ type: "string", minLength: 2 });
    assert.equal(validator.validate("é").valid, false);
    assert.equal(validator.validate("ab").valid, true);
  });

  it("字形簇越界报告原串范围，且不能按码点数冒充簇数", () => {
    const text = "a\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}"; // "a" + 家庭簇
    const validator = new Validator({ type: "string", maxLength: 1, maxLengthUnit: "graphemes" });
    const result = validator.validate(text);
    assert.equal(result.valid, false);
    assert.equal(result.errors.length, 1);
    assert.deepEqual(result.errors[0].range, { start: 1, end: 9 });
    assert.equal(result.errors[0].excerpt, "\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}");
  });

  it("码点越界范围映射回原文（组合重音所在位置不同也正确覆盖）", () => {
    const validator = new Validator({
      type: "string",
      normalization: "NFC",
      maxLength: 3,
      maxLengthUnit: "codepoints",
    });
    // NFC 为 "zab" + U+1EDF（4 码点），越界 1 个簇
    const four = validator.validate("zabo\u031B\u0309");
    assert.equal(four.valid, false);
    assert.equal(four.errors[0].message.includes("4 codepoints 超过上限 3"), true);
    // 越界码点是 U+1EDF，原文覆盖 o + horn + hook above
    assert.deepEqual(four.errors[0].range, { start: 3, end: 6 });
  });
});

describe("规范化", () => {
  it("组合重音与预组合在 NFC 下等价，但映射的原位置不同", () => {
    const validator = new Validator({ type: "string", normalization: "NFC", maxLength: 4 });
    assert.equal(validator.validate("café").valid, true);
    assert.equal(validator.validate("cafe\u0301").valid, true);

    const clusterValidator = new Validator({
      type: "string",
      normalization: "NFC",
      maxLength: 3,
      maxLengthUnit: "graphemes",
    });
    const result = clusterValidator.validate("cafe\u0301");
    assert.equal(result.valid, false);
    // 越界簇在规范化串中是预组合 é，原文上覆盖 e+U+0301
    assert.deepEqual(result.errors[0].range, { start: 3, end: 5 });
  });

  it("NFKC 兼容分解生效（全角字母）", () => {
    const validator = new Validator({
      type: "string",
      normalization: "NFKC",
      maxLength: 3,
      maxLengthUnit: "codepoints",
    });
    assert.equal(validator.validate("ＡＢＣ").valid, true); // U+FF21.. -> ABC
  });
});

describe("allowedScripts 脚本集合", () => {
  it("只允许 Latin+Han 时，假名与 emoji 被逐码点拒绝", () => {
    const validator = new Validator({ type: "string", allowedScripts: ["Latin", "Han"] });
    const result = validator.validate("a日ア");
    assert.equal(result.valid, false);
    assert.equal(result.errors.length, 1);
    assert.equal(result.errors[0].keyword, "allowedScripts");
    assert.equal(result.errors[0].instancePath, "#");
    assert.equal(result.errors[0].rulePath, "#/allowedScripts");
    assert.deepEqual(result.errors[0].range, { start: 2, end: 3 });
    assert.equal(result.errors[0].excerpt, "ア");
  });

  it("组合记号（Inherited/Common）需显式放行 Common 才不误报", () => {
    const strict = new Validator({ type: "string", allowedScripts: ["Latin"] });
    assert.equal(strict.validate("e\u0301").valid, false);

    const withInherited = new Validator({ type: "string", allowedScripts: ["Latin", "Common", "Inherited"] });
    assert.equal(withInherited.validate("e\u0301").valid, true);
  });
});

describe("bidiControl 双向控制字符策略", () => {
  it("默认 deny：逐个控制字符报错且保留原文范围", () => {
    const validator = new Validator({ type: "string" });
    const result = validator.validate("x\u200Ey\u2069z");
    assert.equal(result.valid, false);
    assert.equal(result.errors.length, 2);
    assert.equal(result.errors[0].message.includes("U+200E"), true);
    assert.deepEqual(result.errors[0].range, { start: 1, end: 2 });
    assert.deepEqual(result.errors[1].range, { start: 3, end: 4 });
    assert.equal(result.errors[0].excerpt, "U+200E");
  });

  it("显式 allow 时不报错", () => {
    const validator = new Validator({ type: "string", bidiControl: "allow" });
    assert.equal(validator.validate("x\u200E").valid, true);
  });
});

describe("实例路径与规则路径", () => {
  it("嵌套对象与数组中的错误同时给出 instancePath 和 rulePath", () => {
    const schema = {
      type: "object",
      properties: {
        name: { type: "string", maxLength: 2, maxLengthUnit: "graphemes" },
        tags: {
          type: "array",
          maxItems: 2,
          items: { type: "string", bidiControl: "deny" },
        },
      },
      required: ["name"],
    };
    const validator = new Validator(schema);
    const result = validator.validate({
      name: "abc",
      tags: ["ok", "\u200Ebad", "\u200Eextra"],
    } as unknown as Json);

    const byRule = (suffix: string) => result.errors.find((error) => error.rulePath === suffix);
    assert.equal(byRule("#/properties/name/maxLength")?.instancePath, "#/name");
    const bidi = result.errors.find(
      (error) => error.rulePath === "#/properties/tags/items/bidiControl" && error.instancePath === "#/tags/1",
    );
    assert.deepEqual(bidi?.range, { start: 0, end: 1 });

    // 拒绝项不静默删除：extra 作为越界项仍被报告
    const maxItems = byRule("#/properties/tags/maxItems");
    assert.ok(maxItems);
    assert.match(maxItems.message, /被拒项索引 \[2\]/);
    const rejectedItemError = result.errors.find((error) => error.instancePath === "#/tags/2");
    assert.equal(rejectedItemError?.rulePath, "#/properties/tags/items/bidiControl");
    assert.deepEqual(rejectedItemError.range, { start: 0, end: 1 });
  });

  it("required、type、const、enum 错误路径正确", () => {
    const validator = new Validator({
      type: "object",
      required: ["kind"],
      properties: {
        kind: { enum: ["a", "b"] },
        fixed: { const: 1 },
      },
    });
    const result = validator.validate({ fixed: 2 });
    const rules = result.errors.map((error) => error.rulePath).sort();
    assert.deepEqual(rules, ["#/properties/fixed/const", "#/required"]);
    assert.equal(result.errors.find((error) => error.rulePath === "#/required")?.instancePath, "#/kind");

    const typeCheck = new Validator({
      type: "object",
      properties: { n: { type: "string" } },
    });
    const typeResult = typeCheck.validate({ n: 3 });
    assert.equal(typeResult.errors[0].rulePath, "#/properties/n/type");
    assert.equal(typeResult.errors[0].instancePath, "#/n");
  });

  it("type 数组支持 number 容纳 integer", () => {
    const validator = new Validator({ type: ["number", "string"] });
    assert.equal(validator.validate(3).valid, true);
    assert.equal(validator.validate("3").valid, true);
    assert.equal(validator.validate(true).valid, false);
  });

  it("const 允许 null（不与缺省混淆）", () => {
    const validator = new Validator({ const: null });
    assert.equal(validator.validate(null).valid, true);
    assert.equal(validator.validate(0).valid, false);
  });
});

describe("拒绝项不静默删除（maxItems）", () => {
  it("每个越界元素继续按 items 校验并单独报错", () => {
    const validator = new Validator({
      type: "array",
      maxItems: 1,
      items: { type: "string" },
    });
    const result = validator.validate([1, 2, 3]);
    assert.equal(result.valid, false);
    const itemErrors = result.errors.filter((error) => error.rulePath === "#/items/type");
    assert.deepEqual(
      itemErrors.map((error) => error.instancePath),
      ["#/1", "#/2", "#/0"],
    );
  });
});

describe("schema 子集边界（只承诺实现的子集）", () => {
  it("未知关键字在编译期报错并指向规则路径", () => {
    assert.throws(
      () => new Validator({ type: "string", pattern: "^a" }),
      (error: unknown) => {
        assert.ok(error instanceof SchemaError);
        assert.equal((error as SchemaError).rulePath, "#/pattern");
        assert.match((error as Error).message, /子集不支持的关键字：pattern/);
        return true;
      },
    );
  });

  it("未知脚本名与非法取值在编译期报错", () => {
    assert.throws(() => new Validator({ type: "string", allowedScripts: ["Klingon"] }), SchemaError);
    assert.throws(() => new Validator({ type: "string", normalization: "NFKCX" }), SchemaError);
    assert.throws(() => new Validator({ type: "string", bidiControl: "strip" }), SchemaError);
    assert.throws(() => new Validator({ type: "string", maxLength: -1 }), SchemaError);
  });

  it("字符串关键字不得用于非字符串类型", () => {
    assert.throws(() => new Validator({ type: "number", minLength: 1 }), SchemaError);
  });
});
