/**
 * 统一列级泛化 k-匿名分组工具
 *
 * 规则：
 * - 每列有一棵有限的泛化层级（父指针映射），可把原始值提升到祖先。
 * - 方案是“统一列级”的：每列选定一个层级，对该列所有行一致生效，不允许逐行不同层级。
 * - 泛化后按元组分组，每个最终组合至少出现 k 次（默认 k=3）。
 * - 空值独立处理：空值不随层级泛化，只与其他空值归为一组。
 * - 目标：在所有满足 k-匿名的方案中，使总泛化损失最小。
 *   总泛化损失 = 各列 (层级 / 列最大层级) × 该列非空单元数 之和（同时报告原始层级总和）。
 * - 若任何方案都无法满足（含总人数不足 k），明确报告无解及原因。
 */

type Cell = string | null;

interface ColumnSpec {
  name: string;
  /** 值 -> 父值；不在键中的值视为该列层级的根 */
  hierarchy: Record<string, string>;
}

interface InputSpec {
  /** 每组最小人数，默认 3 */
  k?: number;
  columns: ColumnSpec[];
  rows: Cell[][];
}

interface Group {
  tuple: Cell[];
  count: number;
  /** 1 起始的行号 */
  rowIndices: number[];
}

interface Solution {
  /** 每列选定的泛化层级（0 = 原始值） */
  levels: number[];
  /** 每列可达的最大层级 */
  maxLevels: number[];
  groups: Group[];
  /** 原始损失：各列 层级 × 非空单元数 之和 */
  rawLoss: number;
  /** 总泛化损失：各列 (层级 / 最大层级) × 非空单元数 之和 */
  totalLoss: number;
}

type SolveResult =
  | { ok: true; solution: Solution }
  | { ok: false; reason: string };

const NULL_KEY = String.fromCharCode(0) + "NULL";
const KEY_SEP = String.fromCharCode(1);
const NULL_DISPLAY = "(空值)";

/** 计算层级中每个值到根的深度（0 = 根）。检测环。 */
function depthOf(hierarchy: Record<string, string>): Map<string, number> {
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const walk = (value: string): number => {
    const hit = depth.get(value);
    if (hit !== undefined) return hit;
    if (visiting.has(value)) {
      throw new Error(`层级中存在环，涉及值 "${value}"`);
    }
    visiting.add(value);
    const parent = hierarchy[value];
    const d = parent === undefined ? 0 : walk(parent) + 1;
    visiting.delete(value);
    depth.set(value, d);
    return d;
  };
  for (const key of Object.keys(hierarchy)) walk(key);
  return depth;
}

/** 将值向上提升 level 层；到达根后保持不动。 */
function generalize(value: string, level: number, hierarchy: Record<string, string>): string {
  let current = value;
  for (let i = 0; i < level; i++) {
    const parent = hierarchy[current];
    if (parent === undefined) break;
    current = parent;
  }
  return current;
}

function solve(spec: InputSpec): SolveResult {
  const k = spec.k === undefined ? 3 : spec.k;
  const columns = spec.columns;
  const rows = spec.rows;

  if (!Number.isInteger(k) || k < 2) {
    throw new Error("k 必须为不小于 2 的整数");
  }
  for (let i = 0; i < rows.length; i++) {
    if (rows[i].length !== columns.length) {
      throw new Error(`第 ${i + 1} 行的列数与 columns 不一致`);
    }
  }
  if (rows.length < k) {
    return {
      ok: false,
      reason: `总人数 ${rows.length} 小于 k=${k}，无论如何泛化都无法使每组至少 ${k} 人。`,
    };
  }

  const colCount = columns.length;
  const depths = columns.map((col) => depthOf(col.hierarchy));
  const nonNullCounts = columns.map((_, c) => rows.filter((row) => row[c] !== null).length);
  const distinctValues = columns.map((_, c) => {
    const set = new Set<string>();
    for (const row of rows) {
      const v = row[c];
      if (v !== null) set.add(v);
    }
    return Array.from(set);
  });
  const maxLevels = columns.map((_, c) => {
    let max = 0;
    for (const v of distinctValues[c]) {
      const d = depths[c].get(v) || 0;
      if (d > max) max = d;
    }
    return max;
  });

  // 预计算：每列、每个层级下，原始值 -> 泛化值
  const generalized = columns.map((col, c) => {
    const byLevel: Array<Map<string, string>> = [];
    for (let level = 0; level <= maxLevels[c]; level++) {
      const m = new Map<string, string>();
      for (const v of distinctValues[c]) m.set(v, generalize(v, level, col.hierarchy));
      byLevel.push(m);
    }
    return byLevel;
  });

  const evaluate = (levels: number[]) => {
    const groupMap = new Map<string, Group>();
    rows.forEach((row, idx) => {
      const tuple: Cell[] = row.map((v, c) =>
        v === null ? null : (generalized[c][levels[c]].get(v) as string)
      );
      const key = tuple.map((t) => (t === null ? NULL_KEY : t)).join(KEY_SEP);
      let g = groupMap.get(key);
      if (!g) {
        g = { tuple, count: 0, rowIndices: [] };
        groupMap.set(key, g);
      }
      g.count++;
      g.rowIndices.push(idx + 1);
    });
    const groups = Array.from(groupMap.values());
    let feasible = true;
    for (const g of groups) {
      if (g.count < k) {
        feasible = false;
        break;
      }
    }
    let rawLoss = 0;
    let totalLoss = 0;
    for (let c = 0; c < colCount; c++) {
      rawLoss += levels[c] * nonNullCounts[c];
      totalLoss += (maxLevels[c] > 0 ? levels[c] / maxLevels[c] : 0) * nonNullCounts[c];
    }
    return { feasible, groups, rawLoss, totalLoss };
  };

  // 单调性：泛化只会合并分组。若最高层级方案仍不可行，则任何方案都不可行。
  const top = evaluate(maxLevels);
  if (!top.feasible) {
    const bad = top.groups.filter((g) => g.count < k);
    const lines = bad.map(
      (g) =>
        `  [${g.tuple.map((t) => (t === null ? NULL_DISPLAY : t)).join(", ")}] — ${g.count} 人 (行 ${g.rowIndices.join(", ")})`
    );
    return {
      ok: false,
      reason:
        `即使所有列都泛化到最高层级，仍有 ${bad.length} 个分组不足 ${k} 人：\n` +
        lines.join("\n") +
        `\n（空值独立处理，不随层级泛化，无法与其他值合并）`,
    };
  }

  // 穷举所有统一列级方案（层级组合数 = 各列 (maxLevel+1) 之积，对常规准标识符规模足够小）
  let best: Solution | null = null;
  const levels = new Array<number>(colCount).fill(0);
  const enumerate = (c: number): void => {
    if (c === colCount) {
      const r = evaluate(levels);
      if (!r.feasible) return;
      if (
        best === null ||
        r.totalLoss < best.totalLoss - 1e-12 ||
        (Math.abs(r.totalLoss - best.totalLoss) <= 1e-12 && r.rawLoss < best.rawLoss)
      ) {
        best = {
          levels: levels.slice(),
          maxLevels: maxLevels.slice(),
          groups: r.groups,
          rawLoss: r.rawLoss,
          totalLoss: r.totalLoss,
        };
      }
      return;
    }
    for (let level = 0; level <= maxLevels[c]; level++) {
      levels[c] = level;
      enumerate(c + 1);
    }
  };
  enumerate(0);

  if (best === null) {
    throw new Error("内部错误：最高层级方案可行但未枚举到解");
  }
  return { ok: true, solution: best };
}

function formatTuple(tuple: Cell[]): string {
  return `[${tuple.map((t) => (t === null ? NULL_DISPLAY : t)).join(", ")}]`;
}

function formatSolution(spec: InputSpec, result: SolveResult): string {
  const k = spec.k === undefined ? 3 : spec.k;
  const out: string[] = [];
  out.push(`k = ${k}`);
  if (!result.ok) {
    out.push(`无解: ${result.reason}`);
    return out.join("\n");
  }
  const s = result.solution;
  out.push("统一列级泛化方案:");
  spec.columns.forEach((col, c) => {
    out.push(`  ${col.name}: 第 ${s.levels[c]} 级（最大 ${s.maxLevels[c]} 级）`);
  });
  out.push(`分组（共 ${s.groups.length} 组，每组 ≥ ${k} 人）:`);
  s.groups.forEach((g, i) => {
    out.push(`  组 ${i + 1}: ${formatTuple(g.tuple)} — ${g.count} 人 (行 ${g.rowIndices.join(", ")})`);
  });
  out.push(`总泛化损失: ${s.totalLoss.toFixed(4)}（归一化），层级总和: ${s.rawLoss}`);
  return out.join("\n");
}

/* ---------------- CLI ---------------- */

declare const require: any;
declare const module: any;
declare const process: any;

function main(): void {
  const fs = require("fs");
  const arg = process.argv[2];
  const text = arg ? fs.readFileSync(arg, "utf8") : fs.readFileSync(0, "utf8");
  const spec = JSON.parse(text) as InputSpec;
  const result = solve(spec);
  console.log(formatSolution(spec, result));
  if (!result.ok) process.exitCode = 1;
}

if (require.main === module) {
  main();
}

export { solve, formatSolution, InputSpec, SolveResult, Solution };
