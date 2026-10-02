/** 运行样例：SELECT * WHERE a > 10 ORDER BY b LIMIT 10（100 列 × 10000 行） */
import { buildTable, NUM_COLUMNS } from "./fixture.ts";
import {
  executeEagerMaterialization,
  executeLateMaterialization,
  type Query,
} from "./lateMaterialization.ts";

const NUM_ROWS = 10_000;
const query: Query = {
  filterColumn: "a",
  filterOp: ">",
  filterValue: 10,
  orderByColumn: "b",
  order: "asc",
  limit: 10,
};

// ---- 延迟物化 ----
const lateTable = buildTable(NUM_ROWS);
const lateRows = executeLateMaterialization(lateTable, query);
const lateIO = lateTable.totalCellsRead;

// ---- 早期物化基线 ----
const eagerTable = buildTable(NUM_ROWS);
const eagerRows = executeEagerMaterialization(eagerTable, query);
const eagerIO = eagerTable.totalCellsRead;

const saved = ((1 - lateIO / eagerIO) * 100).toFixed(2);

console.log(`表规模: ${NUM_COLUMNS} 列 × ${NUM_ROWS} 行`);
console.log(`查询: SELECT * WHERE a > 10 ORDER BY b LIMIT 10\n`);
console.log(`早期物化 IO（单元格读取数）: ${eagerIO.toLocaleString()}`);
console.log(`延迟物化 IO（单元格读取数）: ${lateIO.toLocaleString()}`);
console.log(`  - 过滤列 a 全扫:        ${lateTable.column("a").cellsRead.toLocaleString()}`);
console.log(`  - 排序列 b 按选择向量读: ${lateTable.column("b").cellsRead.toLocaleString()}`);
console.log(`  - 其余 98 列各读 10 格:  ${lateTable.column("col2").cellsRead} / 列`);
console.log(`IO 节省: ${saved}%\n`);

const consistent =
  JSON.stringify(lateRows) === JSON.stringify(eagerRows) ? "一致 ✓" : "不一致 ✗";
console.log(`两种执行器结果一致性: ${consistent}`);
console.log(`\n胜出第 1 行（截断展示）:`);
const first = lateRows[0];
console.log(
  `  a=${first.a}, b=${first.b}, col2=${String(first.col2).slice(0, 24)}..., col99=${String(first.col99).slice(0, 24)}...`,
);
