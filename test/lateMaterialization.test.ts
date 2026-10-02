import assert from "node:assert/strict";
import { describe, it, beforeEach } from "node:test";
import { buildTable, NUM_COLUMNS } from "../src/fixture.ts";
import {
  executeEagerMaterialization,
  executeLateMaterialization,
  type Query,
} from "../src/lateMaterialization.ts";
import type { ColumnarTable } from "../src/columnar.ts";

const NUM_ROWS = 10_000;
const LIMIT = 10;

const query: Query = {
  filterColumn: "a",
  filterOp: ">",
  filterValue: 10,
  orderByColumn: "b",
  order: "asc",
  limit: LIMIT,
};

let table: ColumnarTable;
beforeEach(() => {
  table = buildTable(NUM_ROWS);
});

describe("延迟物化投影算子", () => {
  it("前中期仅扫描过滤列 a 与排序列 b", () => {
    executeLateMaterialization(table, query);

    // 过滤列 a：全列扫描一次
    assert.equal(table.column("a").cellsRead, NUM_ROWS);
    // 排序列 b：仅按选择向量读取，绝不全扫
    const bReads = table.column("b").cellsRead;
    assert.ok(bReads < NUM_ROWS, `b 读取 ${bReads} 次，应小于全表 ${NUM_ROWS}`);
    assert.ok(bReads > 0);
  });

  it("其余 98 列仅在收敛后按 LIMIT 行数读取", () => {
    executeLateMaterialization(table, query);

    for (const name of table.columnNames) {
      if (name === "a" || name === "b") continue;
      assert.equal(
        table.column(name).cellsRead,
        LIMIT,
        `列 ${name} 应只物化 ${LIMIT} 个单元格`,
      );
    }
  });

  it("相比早期物化节省至少 90% IO", () => {
    const lateRows = executeLateMaterialization(table, query);
    const lateIO = table.totalCellsRead;

    const eagerTable = buildTable(NUM_ROWS);
    executeEagerMaterialization(eagerTable, query);
    const eagerIO = eagerTable.totalCellsRead;

    assert.equal(eagerIO, NUM_COLUMNS * NUM_ROWS); // 基线：100 列全量
    const saving = 1 - lateIO / eagerIO;
    assert.ok(
      saving >= 0.9,
      `IO 节省 ${(saving * 100).toFixed(2)}%，未达到 90%`,
    );
    assert.equal(lateRows.length, LIMIT);
  });

  it("结果与早期物化完全一致（正确性）", () => {
    const lateRows = executeLateMaterialization(table, query);

    const eagerTable = buildTable(NUM_ROWS);
    const eagerRows = executeEagerMaterialization(eagerTable, query);

    assert.deepEqual(lateRows, eagerRows);
  });

  it("结果按 b 升序且全部满足 a > 10", () => {
    const rows = executeLateMaterialization(table, query);

    assert.equal(rows.length, LIMIT);
    for (const row of rows) {
      assert.ok((row.a as number) > 10);
      assert.equal(Object.keys(row).length, NUM_COLUMNS); // SELECT *
    }
    for (let i = 1; i < rows.length; i++) {
      assert.ok((rows[i].b as number) >= (rows[i - 1].b as number));
    }
  });

  it("降序 + 列子集投影同样延迟物化", () => {
    const q: Query = {
      ...query,
      order: "desc",
      limit: 5,
      selectColumns: ["a", "b", "col7", "col99"],
    };
    const rows = executeLateMaterialization(table, q);

    assert.equal(rows.length, 5);
    assert.equal(table.column("col7").cellsRead, 5);
    assert.equal(table.column("col99").cellsRead, 5);
    assert.equal(table.column("col2").cellsRead, 0); // 未投影的列零 IO
    for (let i = 1; i < rows.length; i++) {
      assert.ok((rows[i].b as number) <= (rows[i - 1].b as number));
    }
  });

  it("LIMIT 超过命中行数时返回全部命中行", () => {
    const q: Query = { ...query, filterValue: 98, limit: 1000 };
    const rows = executeLateMaterialization(table, q);

    assert.ok(rows.length < 1000);
    assert.ok(rows.every((r) => (r.a as number) > 98));
    // 昂贵列读取数 = 实际命中行数，而非 LIMIT
    assert.equal(table.column("col2").cellsRead, rows.length);
  });
});
