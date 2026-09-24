import { BranchingHistory } from "../src/history.ts";

function showBranches(doc: BranchingHistory): void {
  for (const b of doc.branches()) {
    console.log(
      `  分支#${b.id}  text=${JSON.stringify(b.text)}  选择区=[${b.selection.anchor}, ${b.selection.focus}]`,
    );
  }
}

console.log("=== 1. 撤销后继续编辑产生新历史分支 ===");
const doc = new BranchingHistory();
doc.insert("a");
doc.insert("b"); // 连续键入，自动合并为一个节点
console.log(`键入 a、b 后: text=${JSON.stringify(doc.text)}, 节点数=${doc.nodeCount()}（合并为 1 个编辑节点）`);
const abId = doc.currentNodeId;
doc.undo();
console.log(`undo 后: text=${JSON.stringify(doc.text)}`);
doc.insert("x"); // 在撤销点继续编辑 → 新分支
const xId = doc.currentNodeId;
console.log("撤销后输入 x，当前分支列表:");
showBranches(doc);

console.log("\n=== 2. 分支间切换，文本与选择区一起恢复 ===");
doc.switchTo(abId);
console.log(`切到 ab 分支: text=${JSON.stringify(doc.text)}, 选择区=[${doc.selectionRange.anchor}, ${doc.selectionRange.focus}]`);
doc.switchTo(xId);
console.log(`切到 x  分支: text=${JSON.stringify(doc.text)}, 选择区=[${doc.selectionRange.anchor}, ${doc.selectionRange.focus}]`);

console.log("\n=== 3. 合并边界：光标移动 / 显式事务 ===");
const doc2 = new BranchingHistory();
doc2.insert("a");
doc2.commit(); // 显式事务边界
doc2.insert("b");
doc2.setSelection(0); // 光标移动
doc2.insert("c");
console.log(`a |commit| b |move| c → text=${JSON.stringify(doc2.text)}, 编辑节点数=${doc2.nodeCount() - 1}（3 段，未合并）`);

console.log("\n=== 4. UTF-16 偏移与代理对保护 ===");
const doc3 = new BranchingHistory("a😀b");
console.log(`初始 "a😀b" 长度=${doc3.text.length}（UTF-16 code unit）`);
doc3.replace(2, 3, ""); // 试图删除半个 emoji
console.log(`删除 [2,3)（切开代理对）→ 自动外扩: text=${JSON.stringify(doc3.text)}`);
doc3.undo();
doc3.setSelection(2); // 光标试图落在代理对中间
console.log(`setSelection(2) → 实际光标=${doc3.selectionRange.focus}`);

console.log("\n=== 5. LF 与 CRLF ===");
const doc4 = new BranchingHistory("one\r\ntwo\nthree");
doc4.replace(5, 5, "TWO2\r\n");
console.log(`CRLF 后插入: ${JSON.stringify(doc4.text)}`);
doc4.replace(11, 15, "");
console.log(`删除 LF 行:   ${JSON.stringify(doc4.text)}`);

console.log("\n=== 6. 容量回收不破坏存活分支基线 ===");
const doc5 = new BranchingHistory("", { maxNodes: 4 });
doc5.insert("ab");
doc5.commit();
doc5.insert("cd"); // 分支1: abcd
doc5.undo();
doc5.insert("xy"); // 分支2: abxy
console.log(`容量=4，当前节点数=${doc5.nodeCount()}`);
console.log("压缩前回放:");
showBranches(doc5);
const reclaimed = doc5.compact();
console.log(`compact() 回收 ${reclaimed} 个节点，剩余 ${doc5.nodeCount()} 个`);
console.log("压缩后回放每个可见节点:");
for (const n of doc5.nodes()) {
  console.log(`  节点#${n.id} parent=#${n.parentId ?? "-"} text=${JSON.stringify(doc5.textAt(n.id))}`);
}
console.log("压缩后分支:");
showBranches(doc5);
