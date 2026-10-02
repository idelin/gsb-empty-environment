/**
 * 运行样例：完整走一遍验收场景。
 *   1. 已知 A 与 A->B，推导出 B；
 *   2. 加入 C 与 C->B，撤销 A，B 因 C 保留；
 *   3. 撤销 C，B 失去全部支持被安全清除。
 */
import { ReasoningEngine, type ProofNode } from "./engine.ts";

function render(node: ProofNode, indent = ""): string {
  const tag = node.ruleId === null ? "基础事实" : `由 ${node.ruleId} 推出`;
  const lines = [`${indent}${node.fact}  (${tag})`];
  for (const child of node.children) lines.push(render(child, indent + "  "));
  return lines.join("\n");
}

function show(engine: ReasoningEngine, title: string): void {
  console.log(`\n=== ${title} ===`);
  console.log(`当前成立的事实: [${engine.knownFacts().join(", ")}]`);
  const exp = engine.explain("B");
  if (exp) {
    console.log(`B 的最小解释: 证据集 {${exp.baseFacts.join(", ")}}, 推理深度 ${exp.depth}`);
    console.log(render(exp.proof));
  } else {
    console.log("B 不成立，无解释。");
  }
}

const engine = new ReasoningEngine();

// 步骤 1：A 与 A->B
engine.addFact("A");
engine.addRule(["A"], "B");
show(engine, "步骤 1: 断言 A 与规则 A->B");

// 步骤 2：加入 C 与 C->B，撤销 A
engine.addFact("C");
engine.addRule(["C"], "B");
engine.retractFact("A");
show(engine, "步骤 2: 加入 C 与 C->B 后撤销 A（B 因 C 保留）");

// 步骤 3：撤销 C
engine.retractFact("C");
show(engine, "步骤 3: 再撤销 C（B 失去全部支持被清除）");
