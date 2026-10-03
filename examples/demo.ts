import { DynamicSCC, type SCCDiff } from "../src/incremental-scc.ts";

const g = new DynamicSCC();

function show(label: string, diff: SCCDiff): void {
  console.log(`\n=== ${label} ===`);
  for (const m of diff.merged) {
    console.log(
      `  合并: [${m.absorbed.map((c) => c.id).join(", ")}] -> 分量 ${m.component.id} { ${m.component.members.join(", ")} }`,
    );
  }
  for (const s of diff.split) {
    console.log(
      `  分裂: 分量 ${s.from.id} { ${s.from.members.join(", ")} } ->`,
      s.components.map((c) => `${c.id}{ ${c.members.join(", ")} }`).join(", "),
    );
  }
  for (const r of diff.removed) {
    console.log(`  移除: 分量 ${r.id} { ${r.members.join(", ")} }`);
  }
  if (diff.merged.length + diff.split.length + diff.removed.length === 0) {
    console.log("  (无分量变化)");
  }
  console.log(
    "  当前分量:",
    g.getComponents().map((c) => `${c.id}{ ${c.members.join(", ")} }`).join("  "),
  );
}

show("插入 A->B", g.addEdge("A", "B"));
show("插入 B->C", g.addEdge("B", "C"));
show("插入 C->A (三角形环闭合)", g.addEdge("C", "A"));
show("新增独立节点 D", g.addVertex("D"));
show("插入 C->D", g.addEdge("C", "D"));
show("删除 B->C (环断开)", g.removeEdge("B", "C"));
