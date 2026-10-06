import { test } from "node:test";
import assert from "node:assert/strict";
import { runEdit, runBackspace, runForwardDelete } from "./helpers.ts";

test("输入数字：分隔符移动时光标跟随", () => {
  // "123|4" 输入 5 -> raw "1235|4"，display "12,35|4"
  assert.deepEqual(runEdit("123|4", "5"), { raw: "1235|4", display: "12,35|4" });
  // 末尾输入触发新逗号，光标越过逗号
  assert.deepEqual(runEdit("1234|", "5"), { raw: "12345|", display: "12,345|" });
  assert.deepEqual(runEdit("123|", "4"), { raw: "1234|", display: "1,234|" });
});

test("任意选区替换：跨分隔符选区", () => {
  // raw "1234567" 选中 "2345" 输入 "9" -> "19|67"
  assert.deepEqual(runEdit("1[2345]67", "9"), { raw: "19|67", display: "1,9|67" });
  // 全选替换
  assert.deepEqual(runEdit("[1234567]", "8"), { raw: "8|", display: "8|" });
  // 选区删除
  assert.deepEqual(runEdit("12[34]5", ""), { raw: "12|5", display: "12|5" });
});

test("负号规则", () => {
  assert.deepEqual(runEdit("|", "-"), { raw: "-|", display: "-|" });
  assert.deepEqual(runEdit("|123", "-"), { raw: "-|123", display: "-|123" });
  // 中间输入负号被丢弃，光标不动
  assert.deepEqual(runEdit("12|3", "-"), { raw: "12|3", display: "12|3" });
  // 已有负号时再次输入被丢弃
  assert.deepEqual(runEdit("-|123", "-"), { raw: "-|123", display: "-|123" });
  // 负号中间态可继续编辑
  assert.deepEqual(runEdit("-|", "5"), { raw: "-5|", display: "-5|" });
  assert.deepEqual(runBackspace("-|"), { raw: "|", display: "|" });
});

test("小数点规则与中间态", () => {
  assert.deepEqual(runEdit("12|3", "."), { raw: "12.|3", display: "12.|3" });
  assert.deepEqual(runEdit("12|", "."), { raw: "12.|", display: "12.|" });
  assert.deepEqual(runEdit("|", "."), { raw: ".|", display: ".|" });
  // 已有小数点，再次输入被丢弃
  assert.deepEqual(runEdit("1.2|3", "."), { raw: "1.2|3", display: "1.2|3" });
  // 尾点中间态上继续输入
  assert.deepEqual(runEdit("12.|", "5"), { raw: "12.5|", display: "12.5|" });
  // 删除尾点回到整数
  assert.deepEqual(runBackspace("12.|"), { raw: "12|", display: "12|" });
});

test("退格与前删自动跨过千位逗号", () => {
  // display "1,234|" 退格 -> "123|"
  assert.deepEqual(runBackspace("1234|"), { raw: "123|", display: "123|" });
  // display "12,345|" 退格 -> "1,234|"
  assert.deepEqual(runBackspace("12345|"), { raw: "1234|", display: "1,234|" });
  // 光标恰在逗号后（raw "12|345"），退格删掉逗号前的数字
  assert.deepEqual(runBackspace("12|345"), { raw: "1|345", display: "1|,345" });
  // 光标后紧跟逗号时前删，跳过逗号删数字
  assert.deepEqual(runForwardDelete("12|345"), { raw: "12|45", display: "1,2|45" });
  assert.deepEqual(runForwardDelete("1|234"), { raw: "1|34", display: "1|34" });
  // 边界：开头退格 / 末尾前删为无操作
  assert.deepEqual(runBackspace("|123"), { raw: "|123", display: "|123" });
  assert.deepEqual(runForwardDelete("123|"), { raw: "123|", display: "123|" });
});

test("粘贴：任意文本经净化后插入", () => {
  // 含千位逗号与单位的粘贴
  assert.deepEqual(runEdit("|", "￥1,234,567.89元"), {
    raw: "1234567.89|",
    display: "1,234,567.89|",
  });
  // 粘贴到选区上（替换）
  assert.deepEqual(runEdit("12[34]56", "7,890"), {
    raw: "127890|56",
    display: "12,789,0|56",
  });
  // 粘贴内容中的非法字符被丢弃且光标落在存活字符之后
  assert.deepEqual(runEdit("12|34", "a-5.6.7"), {
    raw: "125.67|34",
    display: "125.67|34",
  });
  // 粘贴纯负号得到中间态
  assert.deepEqual(runEdit("|", "-"), { raw: "-|", display: "-|" });
});

test("连续编辑序列：逐步打字与删除", () => {
  let marked = "|";
  const type = (s: string) => {
    for (const ch of s) marked = runEdit(marked, ch).raw;
    return marked;
  };
  assert.equal(type("1234567.89"), "1234567.89|");
  // 退格三次删掉 ".89"... 实际删 '9','8','5'? 逐字符验证
  marked = runBackspace(marked).raw;
  assert.equal(marked, "1234567.8|");
  marked = runBackspace(marked).raw;
  assert.equal(marked, "1234567.|"); // 尾点中间态保留
  marked = runBackspace(marked).raw;
  assert.equal(marked, "1234567|");
  // 在开头补负号
  marked = runEdit("|" + marked.replace("|", ""), "-").raw;
  assert.equal(marked, "-|1234567");
});
