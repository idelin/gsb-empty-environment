import { makeGrammar, computeFirst, computeFollow } from "./grammar.ts";
import { buildLR1Table } from "./lr1.ts";
import { diagnose } from "./diagnose.ts";

/** 经典 Dangling-Else 文法 */
export function danglingElseGrammar() {
  return makeGrammar("S", [
    ["S", "if", "E", "then", "S", "else", "S"],
    ["S", "if", "E", "then", "S"],
    ["S", "other"],
    ["E", "e"],
  ]);
}

function main(): void {
  const g = danglingElseGrammar();
  const first = computeFirst(g);
  const follow = computeFollow(g, first);

  console.log("=== First 集 ===");
  for (const [nt, set] of first) {
    console.log(`  FIRST(${nt}) = { ${[...set].join(", ")} }`);
  }
  console.log("\n=== Follow 集 ===");
  for (const [nt, set] of follow) {
    console.log(`  FOLLOW(${nt}) = { ${[...set].join(", ")} }`);
  }

  const table = buildLR1Table(g);
  console.log(`\n=== LR(1) 自动机：共 ${table.states.length} 个状态 ===`);

  console.log("\n=== 冲突诊断报告 ===");
  console.log(diagnose(table));
}

if (import.meta.filename === process.argv[1]) {
  main();
}
