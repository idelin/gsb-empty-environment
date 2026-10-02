/** 构建 100 列测试表：a（过滤列）、b（排序列）为数值，其余 98 列为大字符串。 */
import { Column, ColumnarTable } from "./columnar.ts";

export const NUM_COLUMNS = 100;
export const BIG_STRING_SIZE = 1024; // 每单元格 1KB，模拟昂贵列

export function buildTable(numRows: number, seed = 42): ColumnarTable {
  // 简单可复现的伪随机数
  let state = seed;
  const rand = () => {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    return state / 0x7fffffff;
  };

  const table = new ColumnarTable(numRows);
  const a: number[] = [];
  const b: number[] = [];
  for (let i = 0; i < numRows; i++) {
    a.push(Math.floor(rand() * 100)); // 0..99，约 90% 满足 a > 10
    b.push(Math.floor(rand() * 100000));
  }
  table.addColumn(new Column("a", a));
  table.addColumn(new Column("b", b));

  const filler = "x".repeat(BIG_STRING_SIZE);
  for (let c = 2; c < NUM_COLUMNS; c++) {
    const data: string[] = [];
    for (let i = 0; i < numRows; i++) data.push(`col${c}-row${i}-${filler}`);
    table.addColumn(new Column(`col${c}`, data));
  }
  return table;
}
