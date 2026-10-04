// 由 anonymize.ts 手工转译（本环境无 tsc，无法离线编译 TypeScript）。
// 逻辑与 anonymize.ts 保持一致，仅去除了类型标注。
"use strict";

const NULL_KEY = String.fromCharCode(0) + "NULL";
const KEY_SEP = String.fromCharCode(1);
const NULL_DISPLAY = "(空值)";

function depthOf(hierarchy) {
  const depth = new Map();
  const visiting = new Set();
  const walk = (value) => {
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

function generalize(value, level, hierarchy) {
  let current = value;
  for (let i = 0; i < level; i++) {
    const parent = hierarchy[current];
    if (parent === undefined) break;
    current = parent;
  }
  return current;
}

function solve(spec) {
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
    const set = new Set();
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

  const generalized = columns.map((col, c) => {
    const byLevel = [];
    for (let level = 0; level <= maxLevels[c]; level++) {
      const m = new Map();
      for (const v of distinctValues[c]) m.set(v, generalize(v, level, col.hierarchy));
      byLevel.push(m);
    }
    return byLevel;
  });

  const evaluate = (levels) => {
    const groupMap = new Map();
    rows.forEach((row, idx) => {
      const tuple = row.map((v, c) =>
        v === null ? null : generalized[c][levels[c]].get(v)
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

  let best = null;
  const levels = new Array(colCount).fill(0);
  const enumerate = (c) => {
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

function formatTuple(tuple) {
  return `[${tuple.map((t) => (t === null ? NULL_DISPLAY : t)).join(", ")}]`;
}

function formatSolution(spec, result) {
  const k = spec.k === undefined ? 3 : spec.k;
  const out = [];
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

function main() {
  const fs = require("fs");
  const arg = process.argv[2];
  const text = arg ? fs.readFileSync(arg, "utf8") : fs.readFileSync(0, "utf8");
  const spec = JSON.parse(text);
  const result = solve(spec);
  console.log(formatSolution(spec, result));
  if (!result.ok) process.exitCode = 1;
}

if (require.main === module) {
  main();
}

module.exports = { solve, formatSolution };
