import { makeWideSchema } from "../src/schema.ts";
import { Join, Project, Scan, outputNames } from "../src/operators.ts";
import { ProjectionPruner, snapshotSchemas } from "../src/pruner.ts";

// 场景：20 列宽表 users，顶层只取 id、name，中间 Join 以 id 为连接键。
const users = new Scan("users", makeWideSchema(20));
const dept = new Scan("dept", [
  { name: "id", type: "BIGINT" },
  { name: "dept_name", type: "VARCHAR" },
  { name: "budget", type: "DECIMAL" },
]);
const join = new Join(users, dept, ["id"], ["id"]);
const project = new Project(join, [
  { name: "id", refs: ["id"] },
  { name: "name", refs: ["name"] },
]);

const show = (label: string) => {
  console.log(`\n=== ${label} ===`);
  for (const node of snapshotSchemas(project)) {
    console.log(`${node.kind.padEnd(8)} -> [${node.schema.map((f) => f.name).join(", ")}]`);
  }
};

show("修剪前（Before Pruning）");

const report = new ProjectionPruner().prune(project, new Set(["id", "name"]));

show("修剪后（After Pruning）");

console.log("\n=== 修剪报告（Prune Report） ===");
for (const scan of report.scans) {
  console.log(
    `Scan(${scan.table}): ${scan.before.length} 列 -> ${scan.after.length} 列 ` +
      `[${scan.after.join(", ")}]，裁掉 ${scan.before.length - scan.after.length} 列`,
  );
}
console.log(`累计裁掉 ${report.prunedColumnCount} 列，降低 Scan 内存与 I/O 开销。`);
console.log(`users 最终输出模式: [${outputNames(users).join(", ")}]`);
