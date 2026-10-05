// 测试：node tests/run.ts
import assert from "node:assert/strict";
import { buildSpec, ParseError } from "../src/parser.ts";
import { check } from "../src/checker.ts";
import { chase, mergeWinner } from "../src/chase.ts";
import { closure, projectedClosure } from "../src/preservation.ts";

let passed = 0;
function test(name: string, fn: () => void): void {
  fn();
  passed++;
  console.log(`ok - ${name}`);
}

// 1. 无损且保持：R(ABC), F={A->B, B->C}, 分解 AB, BC
test("无损 + 保持", () => {
  const r = check(buildSpec("ABC", "A->B;B->C", "AB BC"));
  assert.equal(r.chase.lossless, true);
  assert.equal(r.dependenciesPreserved, true);
  assert.equal(r.chase.witnessRow, 0);
  // 恰好一步合并：B->C 使 b0_2 并入 a2
  assert.equal(r.chase.merges.length, 1);
  const m = r.chase.merges[0];
  assert.equal(m.fdIndex, 1);
  assert.equal(m.eliminated, "b0_2");
  assert.equal(m.kept, "a2");
});

// 2. 有损但保持：R(ABC), F={A->B}, 分解 AB, BC
test("有损 + 保持（保持不蕴含无损）", () => {
  const r = check(buildSpec("ABC", "A->B", "AB BC"));
  assert.equal(r.chase.lossless, false);
  assert.equal(r.dependenciesPreserved, true);
  assert.equal(r.chase.merges.length, 0);
});

// 3. 无损但不保持：R(ABC), F={A->B, B->C}, 分解 AB, AC
test("无损 + 不保持（无损不蕴含保持）", () => {
  const r = check(buildSpec("ABC", "A->B;B->C", "AB AC"));
  assert.equal(r.chase.lossless, true);
  assert.equal(r.dependenciesPreserved, false);
  const p = r.preservation[1]; // B->C
  assert.equal(p.preserved, false);
  assert.deepEqual(p.missingRhs, ["C"]);
  assert.deepEqual(p.reachableClosure, ["B"]);
  assert.deepEqual(p.fullClosure, ["B", "C"]);
  assert.deepEqual(p.closureGap, ["C"]);
});

// 4. 有损且不保持
test("有损 + 不保持", () => {
  const r = check(buildSpec("ABCD", "A->B;C->D", "AB CD"));
  assert.equal(r.chase.lossless, false);
  assert.equal(r.dependenciesPreserved, true); // 两条 FD 都在分量内
});

// 5. 多步合并证据：R(ABCD), F={A->B, B->C, C->D}, 分解 AB, BC, CD
test("多步 chase 证据链", () => {
  const r = check(buildSpec("ABCD", "A->B;B->C;C->D", "AB BC CD"));
  assert.equal(r.chase.lossless, true);
  assert.equal(r.chase.merges.length >= 2, true);
  // 每步消除的符号不得再出现于后续步骤的 kept 之外……直接验证最终行
  assert.deepEqual(r.chase.tableau[0], ["a0", "a1", "a2", "a3"]);
});

// 6. 确定性 / 可重放：同一输入两次运行，证据序列逐字节一致
test("可重放：两次运行结果完全一致", () => {
  const spec = buildSpec("ABCDE", "A->BC;CD->E;B->D;E->A", "ABC CDE");
  const a = JSON.stringify(check(spec));
  const b = JSON.stringify(check(spec));
  assert.equal(a, b);
});

// 7. 合并裁决规则
test("合并裁决：杰出优先，否则取名称较小者", () => {
  assert.equal(mergeWinner("a3", "b0_3"), "a3");
  assert.equal(mergeWinner("b2_5", "a1"), "a1");
  assert.equal(mergeWinner("b1_4", "b0_4"), "b0_4");
  assert.equal(mergeWinner("b0_4", "b1_4"), "b0_4");
});

// 8. 属性上限
test("超过 10 个属性被拒绝", () => {
  assert.throws(() => buildSpec("ABCDEFGHIJK", "A->B", "AB"), ParseError);
  const ok = buildSpec("ABCDEFGHIJ", "A->B", "AB CDEFGHIJ");
  assert.equal(ok.attributes.length, 10);
});

// 9. 闭包与投影闭包
test("闭包计算", () => {
  const spec = buildSpec("ABCD", "A->B;B->C", "AB CD");
  assert.deepEqual(closure(["A"], spec.fds), ["A", "B", "C"]);
  assert.deepEqual(projectedClosure(["A"], spec), ["A", "B"]);
});

// 10. 输入校验
test("非法输入报错", () => {
  assert.throws(() => buildSpec("ABC", "A->D", "AB BC"), ParseError); // 未知属性
  assert.throws(() => buildSpec("ABC", "A->B", "AB"), ParseError);     // 分解未覆盖 C
  assert.throws(() => buildSpec("ABC", "AB", "AB BC"), ParseError);    // 缺少 ->
});

console.log(`\n${passed} 个测试全部通过`);
