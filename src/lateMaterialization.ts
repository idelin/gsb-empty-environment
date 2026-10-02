/**
 * 延迟物化（Late Materialization）投影算子。
 *
 * 针对形如  SELECT * WHERE a > 10 ORDER BY b LIMIT 10  的 OLAP 查询：
 *
 *   阶段 1（过滤）：仅扫描过滤列 a，生成选择向量（selection vector）。
 *   阶段 2（排序）：仅对选中行读取排序列 b，排序后应用 LIMIT，
 *                   得到最终胜出的少数物理行号。
 *   阶段 3（物化）：直到此时，才按胜出 rowId 回表读取其余 98 个
 *                   昂贵列（大字符串等），每列仅读取 LIMIT 个单元格。
 *
 * 对比早期物化（Eager）：先读出全部 100 列再过滤排序，
 * IO 量为 列数 × 行数；延迟物化可节省 90% 以上 IO。
 */
import type { CellValue, ColumnarTable } from "./columnar.ts";

export interface Query {
  filterColumn: string;
  filterOp: ">" | ">=" | "<" | "<=" | "=";
  filterValue: number;
  orderByColumn: string;
  order?: "asc" | "desc";
  limit: number;
  /** SELECT * 时表示全部列 */
  selectColumns?: string[];
}

export type Row = Record<string, CellValue>;

function compare(op: Query["filterOp"], lhs: CellValue, rhs: number): boolean {
  const v = lhs as number;
  switch (op) {
    case ">": return v > rhs;
    case ">=": return v >= rhs;
    case "<": return v < rhs;
    case "<=": return v <= rhs;
    case "=": return v === rhs;
  }
}

/**
 * 延迟物化执行器。
 * 返回结果行；各列的 IO 计数可通过 table 上的 cellsRead 观察。
 */
export function executeLateMaterialization(
  table: ColumnarTable,
  query: Query,
): Row[] {
  const order = query.order ?? "asc";
  const selectColumns = query.selectColumns ?? table.columnNames;

  // ---- 阶段 1：仅扫描过滤列，生成选择向量 ----
  const filterCol = table.column(query.filterColumn);
  const filterValues = filterCol.scanAll();
  const selectionVector: number[] = [];
  for (let rowId = 0; rowId < table.numRows; rowId++) {
    if (compare(query.filterOp, filterValues[rowId], query.filterValue)) {
      selectionVector.push(rowId);
    }
  }

  // ---- 阶段 2：仅对选中行读取排序列，排序 + LIMIT 收敛 ----
  const sortCol = table.column(query.orderByColumn);
  const sortKeys = sortCol.readMany(selectionVector);
  const keyed = selectionVector.map((rowId, i) => ({
    rowId,
    key: sortKeys[i] as number,
  }));
  keyed.sort((x, y) => (order === "asc" ? x.key - y.key : y.key - x.key));
  const winners = keyed.slice(0, query.limit).map((k) => k.rowId);

  // ---- 阶段 3：收敛后才物化其余列，每列仅读取胜出行的单元格 ----
  const deferredColumns = selectColumns.filter(
    (name) => name !== query.filterColumn && name !== query.orderByColumn,
  );
  const materialized = new Map<string, CellValue[]>();
  for (const name of deferredColumns) {
    materialized.set(name, table.column(name).readMany(winners));
  }

  // 组装结果行（过滤列/排序列复用已读数据，不产生额外 IO）
  const filterByRow = new Map<number, CellValue>();
  for (const rowId of selectionVector) {
    filterByRow.set(rowId, filterValues[rowId]);
  }
  const sortByRow = new Map<number, CellValue>();
  keyed.forEach(({ rowId, key }, ) => sortByRow.set(rowId, key));

  return winners.map((rowId, i) => {
    const row: Row = {};
    for (const name of selectColumns) {
      if (name === query.filterColumn) row[name] = filterByRow.get(rowId)!;
      else if (name === query.orderByColumn) row[name] = sortByRow.get(rowId)!;
      else row[name] = materialized.get(name)![i];
    }
    return row;
  });
}

/** 早期物化（Eager）基线：先读出所有 SELECT 列的全部行，再过滤排序。 */
export function executeEagerMaterialization(
  table: ColumnarTable,
  query: Query,
): Row[] {
  const order = query.order ?? "asc";
  const selectColumns = query.selectColumns ?? table.columnNames;

  const columnData = new Map<string, CellValue[]>();
  for (const name of selectColumns) {
    columnData.set(name, table.column(name).scanAll());
  }

  const rows: Row[] = [];
  for (let rowId = 0; rowId < table.numRows; rowId++) {
    if (
      compare(
        query.filterOp,
        columnData.get(query.filterColumn)![rowId],
        query.filterValue,
      )
    ) {
      const row: Row = {};
      for (const name of selectColumns) row[name] = columnData.get(name)![rowId];
      rows.push(row);
    }
  }
  rows.sort((x, y) => {
    const a = x[query.orderByColumn] as number;
    const b = y[query.orderByColumn] as number;
    return order === "asc" ? a - b : b - a;
  });
  return rows.slice(0, query.limit);
}
