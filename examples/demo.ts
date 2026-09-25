import {
  ABSENT,
  DELETE,
  formatPath,
  merge3,
} from "../src/index.ts";
import type { Conflict, JsonValue } from "../src/index.ts";

const base = {
  app: "demo",
  version: 1,
  server: { host: "localhost", port: 8080, tls: false },
  features: [
    { id: "search", enabled: true, weight: 1 },
    { id: "chat", enabled: false },
    { id: "legacy", enabled: true },
  ],
};

const ours = {
  app: "demo",
  version: 2,
  server: { host: "example.com", port: 8080 },
  features: [
    { id: "search", enabled: true, weight: 5 },
    { id: "chat", enabled: false },
    { id: "beta", enabled: true },
  ],
};

const theirs = {
  app: "demo",
  version: 1,
  server: { host: "localhost", port: 9090, tls: true },
  features: [
    { id: "search", enabled: false, weight: 1 },
    { id: "chat", enabled: true },
    { id: "legacy", enabled: false },
  ],
};

function show(value: JsonValue | typeof ABSENT): string {
  return value === ABSENT ? "<deleted>" : JSON.stringify(value);
}

function printConflicts(conflicts: Conflict[]): void {
  for (const conflict of conflicts) {
    console.log(`  路径   ${formatPath(conflict.path)}`);
    console.log(`    base   : ${show(conflict.base)}`);
    console.log(`    ours   : ${show(conflict.ours)}`);
    console.log(`    theirs : ${show(conflict.theirs)}`);
  }
}

console.log("=== 第一次合并（无人工解决） ===");
const first = merge3(base, ours, theirs);
console.log(`ok = ${first.ok}，冲突数 = ${first.conflicts.length}`);
printConflicts(first.conflicts);
console.log("\n暂定合并树（冲突处暂取 ours，ours 已删则取 theirs）：");
console.log(JSON.stringify(first.tree, null, 2));

console.log("\n=== 逐项人工解决后重新合并 ===");
const resolved = merge3(base, ours, theirs, {
  resolutions: [
    { path: "$.server.tls", value: DELETE },
    { path: '$.features[id="legacy"]', value: { id: "legacy", enabled: false } },
  ],
});
console.log(`ok = ${resolved.ok}，冲突数 = ${resolved.conflicts.length}`);
console.log("最终合并树：");
console.log(JSON.stringify(resolved.tree, null, 2));

console.log("\n=== 非法输入：重复 id 被拒绝 ===");
try {
  merge3(base, { features: [{ id: "x" }, { id: "x" }] }, theirs);
} catch (err) {
  console.log(`  ${(err as Error).name}: ${(err as Error).message}`);
}
