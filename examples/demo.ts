/**
 * 运行样例：node examples/demo.ts
 * 演示 Peritext 风格格式标注 CRDT 的并发合并行为。
 */
import { RichText, type Attrs } from "../src/richtext.ts";

function fmtMarks(marks: Attrs): string {
  const parts = Object.entries(marks).map(([k, v]) => (v === true ? k : `${k}=${v}`));
  return parts.length > 0 ? parts.join(", ") : "(无格式)";
}

function show(label: string, doc: RichText): void {
  console.log(`  ${label}`);
  for (const s of doc.getSpans()) {
    console.log(`    [${s.start}, ${s.end}) "${s.text}"  ->  ${fmtMarks(s.marks)}`);
  }
}

function sync(...docs: RichText[]): void {
  const allOps = docs.flatMap((d) => d.getOps());
  for (const d of docs) d.apply(allOps);
}

console.log("=== 场景 1（验收）: 'ABC' 并发加粗 'AB' + 下划线 'BC' ===");
{
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.format(0, 2, "bold", true); // 用户1: 'AB' 加粗
  bob.format(1, 2, "underline", true); // 用户2: 'BC' 下划线（并发）

  sync(alice, bob);

  show("alice 副本:", alice);
  show("bob 副本:", bob);
  console.log(`  两个副本一致: ${JSON.stringify(alice.getSpans()) === JSON.stringify(bob.getSpans())}`);
}

console.log("\n=== 场景 2: 并发冲突 —— A 加粗 vs B 取消加粗（同区间） ===");
{
  const alice = RichText.from("ABC", "alice");
  alice.format(0, 3, "bold", true); // 共同祖先：全文已加粗
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.format(0, 3, "bold", null); // alice: 取消加粗
  bob.format(0, 3, "bold", true); // bob:   并发重新加粗

  sync(alice, bob);
  show("合并结果（lamport 相同，actor 'bob' > 'alice'，加粗获胜）:", alice);

  // 对照：若取消加粗发生在因果更晚（看到重新加粗之后），则取消获胜
  const carol = RichText.fromOps("carol", alice.getOps());
  carol.format(0, 3, "bold", null); // carol 看到上述全部历史后再取消
  sync(alice, carol);
  show("因果更晚的取消加粗获胜:", alice);
}

console.log("\n=== 场景 3: 重叠超链接按 LWW 逐字符裁决 ===");
{
  const alice = RichText.from("ABC", "alice");
  const bob = RichText.fromOps("bob", alice.getOps());

  alice.format(0, 2, "link", "https://a.example");
  bob.format(1, 2, "link", "https://b.example");

  sync(alice, bob);
  show("合并结果:", alice);
}

console.log("\n=== 场景 4: 边界扩展 —— 在加粗区间末尾键入继承格式 ===");
{
  const doc = RichText.from("ABC", "alice");
  doc.format(0, 2, "bold", true);
  doc.insert(2, "X"); // 末尾边界 -> 继承加粗
  doc.insert(0, "Y"); // 起始边界 -> 不继承
  show(`键入后文本 "${doc.getText()}":`, doc);
}
