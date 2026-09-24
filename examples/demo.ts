import { TextDocument, type TextNode } from "../src/rope.ts";

function collectNodes(root: TextNode, set = new Set<TextNode>()): Set<TextNode> {
  set.add(root);
  if (root.kind === "branch") {
    collectNodes(root.left, set);
    collectNodes(root.right, set);
  }
  return set;
}

console.log("=== 1. 构建与行列定位 (LF + CRLF + emoji) ===");
const v1 = TextDocument.from("one\ntwo\r\nthree 😀 four\r\nlast");
console.log("text      :", JSON.stringify(v1.getText()));
console.log("length    :", v1.length, "(UTF-16 code units)");
console.log("lineCount :", v1.lineCount);
const emojiOffset = v1.getText().indexOf("😀");
console.log("emoji at offset", emojiOffset, "->", v1.positionAt(emojiOffset));
console.log("after emoji  ", emojiOffset + 2, "->", v1.positionAt(emojiOffset + 2));
console.log("round-trip :", v1.offsetAt(v1.positionAt(emojiOffset + 2)) === emojiOffset + 2);

console.log("\n=== 2. 祖孙三版本：结构共享 + 读取互不污染 ===");
let base = TextDocument.empty();
let baseStr = "";
for (let i = 0; i < 500; i++) baseStr += `row ${i}: some text 😀\r\n`;
base = TextDocument.from(baseStr);
const b1 = base;
const b2 = b1.insert(10_000, "INSERTED ");
const b3 = b2.replace(0, 4, "ROW");
console.log("v1 head:", JSON.stringify(b1.getText().slice(0, 24)), "length", b1.length);
console.log("v2 head:", JSON.stringify(b2.getText().slice(0, 24)), "length", b2.length);
console.log("v3 head:", JSON.stringify(b3.getText().slice(0, 24)), "length", b3.length);
console.log("v1 still intact:", b1.getText() === baseStr);
const [m1, m2, m3] = [collectNodes(b1.root), collectNodes(b2.root), collectNodes(b3.root)];
console.log("nodes v1/v2/v3:", m1.size, m2.size, m3.size);
console.log("shared v1<->v3 :", [...m3].filter((n) => m1.has(n)).length, "of", m1.size, "v1 nodes reused by v3");

console.log("\n=== 3. CRLF 中间插入：按文档定义拒绝 ===");
const crlf = TextDocument.from("a\r\nb");
try {
  crlf.insert(2, "X"); // offset 2 位于 \r 与 \n 之间
} catch (err) {
  console.log("rejected  :", (err as RangeError).name, "-", (err as RangeError).message);
}
console.log("at boundary:", JSON.stringify(crlf.insert(1, "X").getText()), "/", JSON.stringify(crlf.insert(3, "X").getText()));

console.log("\n=== 4. 代理对禁止切开 ===");
const emoji = TextDocument.from("x😀y");
try {
  emoji.insert(2, "Z");
} catch (err) {
  console.log("rejected  :", (err as RangeError).message);
}

console.log("\n=== 5. 范围迭代：不拼接全文 ===");
let big = TextDocument.empty();
for (let i = 0; i < 2000; i++) big = big.insert(big.length, `line ${i} 😀\r\n`);
console.log("big doc   :", big.length, "code units,", big.lineCount, "lines");
const chunks = [...big.chunks(10_000, 10_500)];
console.log("slice     :", chunks.length, "chunks,", chunks.reduce((n, c) => n + c.length, 0), "code units yielded");
console.log("preview   :", JSON.stringify(chunks.join("").slice(0, 60)) + "...");
