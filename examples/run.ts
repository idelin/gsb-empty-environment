/**
 * 运行样例：node --experimental-strip-types examples/run.ts
 */
import { Validator } from "../src/validator.ts";
import type { Json } from "../src/schema.ts";

function show(title: string, result: ReturnType<Validator["validate"]>): void {
  console.log(`\n=== ${title} ===`);
  console.log(result.valid ? "valid: true" : "valid: false");
  for (const error of result.errors) {
    const range = error.range ? `  原文范围=[${error.range.start},${error.range.end}) 片段=${JSON.stringify(error.excerpt)}` : "";
    console.log(`- instancePath=${error.instancePath}  rulePath=${error.rulePath}${range}`);
    console.log(`  ${error.message}`);
  }
}

// 1) 三种长度单位：同一个 emoji ZWJ 家庭簇
const family = "\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}"; // 1 个字形簇 / 5 码点 / 8 UTF-16
for (const unit of ["graphemes", "codepoints", "utf16"] as const) {
  const validator = new Validator({ type: "string", maxLength: 4, maxLengthUnit: unit });
  show(`上限 4（单位=${unit}）：${JSON.stringify(family)}`, validator.validate(family));
}

// 2) 组合重音 vs 预组合：NFC 后等价，但报错映射回的原位置不同
const nfc = new Validator({ type: "string", normalization: "NFC", maxLength: 3 });
show('NFC + maxLength(码点)=3："café"（预组合）', nfc.validate("café"));
show('NFC + maxLength(码点)=3："cafe\\u0301"（组合重音）', nfc.validate("cafe\u0301"));

// 3) 字形簇上限：emoji 不能按码点数冒充簇数；范围映射回原簇
const clusterLimit = new Validator({ type: "string", maxLength: 2, maxLengthUnit: "graphemes" });
show('字形簇上限 2："a" + 挥手深肤色簇 + "b"', clusterLimit.validate("a\u{1F44B}\u{1F3FD}b"));

// 4) 脚本集合 + 双向控制字符（默认 deny，拒绝不删除）
const profile = new Validator({
  type: "object",
  required: ["nickname"],
  properties: {
    nickname: {
      type: "string",
      normalization: "NFC",
      allowedScripts: ["Latin", "Han", "Common", "Inherited"],
      maxLength: 4,
      maxLengthUnit: "graphemes",
    },
  },
});
show("用户资料（含假名 + RLM U+200F + 超长）", profile.validate({
  nickname: "名字ア\u200F長すぎ",
} as Json));

// 5) 数组：被拒越界项逐项报告，不静默删除
const tags = new Validator({
  type: "array",
  maxItems: 2,
  items: { type: "string", bidiControl: "deny" },
});
show('tags 数组 maxItems=2，越界项仍校验', tags.validate(["ok", "\u200Ebad", "\u200Eextra"]));
