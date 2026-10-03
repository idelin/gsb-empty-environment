import { test } from "node:test";
import assert from "node:assert/strict";
import { RichText, type Attrs, type Op, type Span } from "../src/richtext.ts";

/** 全量同步：把所有副本的操作互相应用一遍（模拟网络交换）。 */
function sync(...docs: RichText[]): void {
  const allOps = docs.flatMap((d) => d.getOps());
  for (const d of docs) d.apply(allOps);
}

/** 简化为 [文本, 格式] 二元组，便于断言。 */
function simplify(doc: RichText): Array<[string, Attrs]> {
  return doc.getSpans().map((s: Span) => [s.text, s.marks]);
}

/** 确定性打乱（固定种子的 Fisher-Yates），用于乱序应用测试。 */
function shuffle<T>(arr: T[], seed: number): T[] {
  const a = [...arr];
  let s = seed;
  const rand = () => (s = (s * 1103515245 + 12345) % 2147483648) / 2147483648;
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [a[i], a[j]] = [a[j]!, a[i]!];
  }
  return a;
}

test("验收: 'ABC' 上并发加粗 'AB' 与下划线 'BC'，合并后无格式断裂", () => {
  // 共同祖先：alice 创建文本 'ABC'，同步给 bob
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());

  // 并发标注：alice 给 'AB' 加粗，bob 给 'BC' 加下划线
  alice.format(0, 2, "bold", true);
  bob.format(1, 2, "underline", true);

  sync(alice, bob);

  const expected: Array<[string, Attrs]> = [
    ["A", { bold: true }],
    ["B", { bold: true, underline: true }],
    ["C", { underline: true }],
  ];
  assert.deepEqual(simplify(alice), expected);
  assert.deepEqual(simplify(bob), expected);
  // 文本完整、无断裂
  assert.equal(alice.getText(), "ABC");
  assert.equal(bob.getText(), "ABC");
  assert.deepEqual(
    alice.getSpans().map((s) => [s.start, s.end]),
    [
      [0, 1],
      [1, 2],
      [2, 3],
    ],
  );
});

test("边界因果: 因果上后发生的取消加粗覆盖先前的加粗", () => {
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.format(0, 3, "bold", true); // alice 先加粗全文
  sync(alice, bob); // bob 看到加粗
  bob.format(0, 3, "bold", null); // bob 随后取消加粗（因果更晚）
  sync(alice, bob);

  assert.deepEqual(simplify(alice), [["ABC", {}]]);
  assert.deepEqual(simplify(bob), [["ABC", {}]]);
});

test("边界因果: 并发的加粗与取消加粗按 (lamport, actor) 确定性裁决且收敛", () => {
  const alice = RichText.from("ABC", "alice");
  alice.format(0, 3, "bold", true); // 共同祖先：全文已加粗
  const bob = RichText.fromOps("bob", alice.getOps());

  // 并发冲突：alice 取消加粗，bob 同时重新加粗（二者互不可见，lamport 相同）
  alice.format(0, 3, "bold", null);
  bob.format(0, 3, "bold", true);

  sync(alice, bob);

  // lamport 相同 -> actor 字典序 'bob' > 'alice' -> bob 的加粗获胜
  assert.deepEqual(simplify(alice), [["ABC", { bold: true }]]);
  assert.deepEqual(simplify(bob), [["ABC", { bold: true }]]);
});

test("边界因果: 部分重叠的取消加粗与并发斜体互不干扰", () => {
  const alice = RichText.from("ABC", "alice");
  alice.format(0, 3, "bold", true); // 共同祖先：全文加粗
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.format(0, 2, "bold", null); // alice 取消 'AB' 的加粗
  bob.format(1, 2, "italic", true); // bob 并发给 'BC' 加斜体

  sync(alice, bob);

  const expected: Array<[string, Attrs]> = [
    ["A", {}],
    ["B", { italic: true }],
    ["C", { bold: true, italic: true }],
  ];
  assert.deepEqual(simplify(alice), expected);
  assert.deepEqual(simplify(bob), expected);
});

test("超链接冲突: 重叠区间按 LWW 逐字符裁决", () => {
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.format(0, 2, "link", "https://a.example"); // alice: 'AB' -> a
  bob.format(1, 2, "link", "https://b.example"); // bob:   'BC' -> b（并发）

  sync(alice, bob);

  // 'B' 上两条 link 冲突：lamport 相同，actor 'bob' > 'alice'，b 获胜
  const expected: Array<[string, Attrs]> = [
    ["A", { link: "https://a.example" }],
    ["BC", { link: "https://b.example" }],
  ];
  assert.deepEqual(simplify(alice), expected);
  assert.deepEqual(simplify(bob), expected);
});

test("收敛性: 操作乱序 + 重复应用后结果一致（交换律/结合律/幂等）", () => {
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());
  alice.format(0, 2, "bold", true);
  bob.format(1, 2, "underline", true);
  alice.delete(2, 1); // alice 再删掉 'C'

  const allOps: Op[] = [...alice.getOps(), ...bob.getOps()];
  const reference = RichText.fromOps("ref", allOps);

  for (const seed of [1, 7, 42, 2024]) {
    const shuffled = shuffle(allOps, seed);
    // 重复混入部分操作，验证幂等
    const dup = [...shuffled, ...shuffled.slice(0, 3)];
    const replica = RichText.fromOps(`r${seed}`, dup);
    assert.deepEqual(simplify(replica), simplify(reference));
    assert.equal(replica.getText(), reference.getText());
  }
  assert.equal(reference.getText(), "AB");
});

test("边界扩展: 末尾边界键入继承格式，起始边界不继承", () => {
  const doc = RichText.from("ABC", "alice");
  doc.format(0, 2, "bold", true); // 'AB' 加粗（默认仅末尾扩展）

  doc.insert(2, "X"); // 在加粗区间末尾边界键入 -> 继承加粗
  doc.insert(0, "Y"); // 在加粗区间起始边界键入 -> 不继承

  assert.equal(doc.getText(), "YABXC");
  assert.deepEqual(simplify(doc), [
    ["Y", {}],
    ["ABX", { bold: true }],
    ["C", {}],
  ]);
});

test("并发插入: 同一位置并发插入按字符 ID 确定性排序", () => {
  const alice = RichText.from("AC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.insert(1, "x"); // alice 在 'A' 后插入 'x'
  bob.insert(1, "y"); // bob 并发在 'A' 后插入 'y'

  sync(alice, bob);

  assert.equal(alice.getText(), bob.getText());
  assert.match(alice.getText(), /^A[xy]{2}C$/);
});

test("删除: 删除格式区间内的字符后区间收缩且不留空洞", () => {
  const alice = RichText.from("ABC", "alice");
  alice.format(0, 3, "bold", true);
  const bob = RichText.fromOps("bob", alice.getOps());

  bob.delete(1, 1); // 删除 'B'
  sync(alice, bob);

  assert.equal(alice.getText(), "AC");
  assert.deepEqual(simplify(alice), [["AC", { bold: true }]]);
  assert.deepEqual(simplify(bob), [["AC", { bold: true }]]);
});

test("merge: 两个副本直接互合并与操作传输等价", () => {
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());
  alice.format(0, 2, "bold", true);
  bob.format(1, 2, "underline", true);

  alice.merge(bob);
  bob.merge(alice);

  assert.deepEqual(simplify(alice), simplify(bob));
});
