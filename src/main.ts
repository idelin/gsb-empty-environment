import { buildState, childrenOf, descendantsOf, effectiveAcl } from "./tree.ts";
import {
  assertIdentityPreserved,
  findViolations,
  planMigration,
} from "./planner.ts";
import { scenarioNodes, targetGrouping } from "./scenario.ts";
import type { TreeState } from "./types.ts";

function printTree(state: TreeState): void {
  const walk = (id: string, indent: string): void => {
    const tags: string[] = [];
    if (state.pinned.has(id)) tags.push("固定");
    const acl = [...effectiveAcl(state, id)].sort().join(", ");
    console.log(`${indent}${state.name.get(id)} (${id})${tags.length ? " [" + tags.join(",") + "]" : ""} 权限={${acl}}`);
    for (const child of childrenOf(state, id)) walk(child, indent + "  ");
  };
  walk(state.root, "");
}

function main(): void {
  const initial = buildState(scenarioNodes);
  console.log(`节点总数: ${initial.parent.size}（上限 15）\n`);

  console.log("== 迁移前层级与继承权限 ==");
  printTree(initial);

  console.log("\n== 目标分组要求 ==");
  for (const [node, container] of Object.entries(targetGrouping.mustBeUnder)) {
    console.log(`  ${node} 必须位于 ${container} 之下`);
  }

  const violations = findViolations(initial, targetGrouping);
  console.log(`\n当前违规节点: ${violations.map((v) => v.node).join(", ") || "无"}`);

  const result = planMigration(initial, targetGrouping);
  if (!result) {
    console.log("\n未找到满足目标分组的迁移方案。");
    process.exitCode = 1;
    return;
  }

  assertIdentityPreserved(initial, result.finalState);

  console.log(`\n== 迁移方案（${result.moves.length} 次移动，搜索状态数 ${result.exploredStates}）==`);
  result.moves.forEach((move, i) => {
    const relocated = descendantsOf(initial, move.nodeId).length + 1;
    console.log(`  ${i + 1}. 移动 ${move.nodeId} -> 挂载到 ${move.newParentId} 之下（子树共 ${relocated} 个节点随动）`);
  });

  console.log("\n== 迁移后层级与继承权限 ==");
  printTree(result.finalState);

  console.log(`\n== 权限例外（次目标：受影响节点 ${result.exceptionNodeCount} 个，权限差异条目 ${result.exceptionEntryCount} 条）==`);
  if (result.exceptions.length === 0) console.log("  无");
  for (const ex of result.exceptions) {
    const gained = ex.gained.length ? `新增 {${ex.gained.join(", ")}}` : "";
    const lost = ex.lost.length ? `丢失 {${ex.lost.join(", ")}}` : "";
    console.log(`  ${ex.nodeId}: ${[gained, lost].filter(Boolean).join("，")}`);
  }

  console.log("\n== 引用解析对照（迁移前 -> 迁移后）==");
  const statusText: Record<string, string> = {
    UNCHANGED: "不变",
    RETARGETED: "重定向",
    BROKEN: "失效",
    REPAIRED: "修复",
    STILL_BROKEN: "仍失效",
  };
  for (const r of result.refResolutions) {
    const before = r.before ?? "(无法解析)";
    const after = r.after ?? "(无法解析)";
    console.log(`  ${r.nodeId}: "${r.path}"  ${before} -> ${after}  [${statusText[r.status]}]`);
  }

  console.log("\n校验通过：节点集合不变（无复制虚增身份）、固定节点未移动、目标分组全部满足。");
}

main();
