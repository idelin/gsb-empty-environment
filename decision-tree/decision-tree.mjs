/**
 * decision-tree.ts 的手工转译版（离线环境无 tsc；逻辑与 TS 源逐行对应）。
 * 有 TypeScript 工具链时可直接 `tsc decision-tree.ts cli.ts --target es2020 --module commonjs` 编译运行。
 */

export const MAX_CANDIDATES = 18;

/** 属性取值的规范化键：带上类型前缀，避免 "1" 与 1 之类的碰撞。 */
function valueKey(v) {
  return typeof v + ":" + String(v);
}

export function solveProblem(problem) {
  const { candidates, costs } = problem;
  const n = candidates.length;
  if (n === 0) throw new Error("至少需要一个候选对象。");
  if (n > MAX_CANDIDATES) {
    throw new Error(`候选数 ${n} 超过上限 ${MAX_CANDIDATES}。`);
  }
  const attributes = Object.keys(costs);
  if (attributes.length === 0) throw new Error("至少需要一个可询问属性。");
  for (const a of attributes) {
    if (!(costs[a] > 0)) throw new Error(`属性 ${a} 的成本必须为正数。`);
  }
  for (const c of candidates) {
    for (const a of attributes) {
      if (!(a in c.attrs)) {
        throw new Error(`候选 ${c.id} 缺少属性 ${a}。`);
      }
    }
  }

  // 每个候选的完整属性签名：签名相同 <=> 不可区分。
  const signatures = candidates.map((c) =>
    JSON.stringify(attributes.map((a) => valueKey(c.attrs[a]))),
  );

  // 每个候选在每个属性下的取值键，用于快速分裂；display 为原始取值的展示形式。
  const keys = candidates.map((c) =>
    attributes.map((a) => valueKey(c.attrs[a])),
  );
  const display = candidates.map((c) =>
    attributes.map((a) => String(c.attrs[a])),
  );
  const attrIndex = new Map();
  attributes.forEach((a, i) => attrIndex.set(a, i));

  // 属性按成本升序排列，配合下界剪枝：cost(a) >= 当前最优即可停止。
  const orderedAttrs = [...attributes].sort((a, b) =>
    costs[a] - costs[b] || (a < b ? -1 : a > b ? 1 : 0),
  );

  const memo = new Map();

  function solveCost(mask) {
    const hit = memo.get(mask);
    if (hit) return hit.cost;

    const indices = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) indices.push(i);

    let entry;
    const first = indices[0];
    const allSame = indices.every((i) => signatures[i] === signatures[first]);
    if (indices.length === 1 || allSame) {
      // 单例或等价组：无需再问，叶子成本 0。
      entry = { cost: 0, attribute: null };
    } else {
      let best = Infinity;
      let bestAttr = null;
      for (const a of orderedAttrs) {
        const ca = costs[a];
        if (ca >= best) break; // 下界剪枝：分支子问题成本非负
        const ai = attrIndex.get(a);
        const groups = new Map();
        for (const i of indices) {
          const k = keys[i][ai];
          const g = groups.get(k);
          groups.set(k, (g ?? 0) | (1 << i));
        }
        if (groups.size < 2) continue; // 该属性在当前集合上不产生分裂
        let worst = 0;
        let abort = false;
        for (const sub of groups.values()) {
          const c = solveCost(sub);
          if (c > worst) worst = c;
          if (ca + worst >= best) { abort = true; break; } // 提前放弃该属性
        }
        if (!abort && ca + worst < best) {
          best = ca + worst;
          bestAttr = a;
        }
      }
      // 必然存在至少一个可分裂属性（否则 allSame 已成立）。
      entry = { cost: best, attribute: bestAttr };
    }
    memo.set(mask, entry);
    return entry.cost;
  }

  const fullMask = (1 << n) - 1;
  const worstCaseCost = solveCost(fullMask);

  function idsOf(mask) {
    const ids = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) ids.push(candidates[i].id);
    return ids;
  }

  function buildTree(mask) {
    const entry = memo.get(mask);
    if (entry.attribute === null) {
      return { kind: "leaf", candidates: idsOf(mask) };
    }
    const ai = attrIndex.get(entry.attribute);
    const groups = new Map();
    for (let i = 0; i < n; i++) {
      if (!(mask & (1 << i))) continue;
      const k = keys[i][ai];
      const g = groups.get(k);
      if (g) g.mask |= 1 << i;
      else groups.set(k, { display: display[i][ai], mask: 1 << i });
    }
    const branches = [...groups.values()]
      .sort((x, y) => (x.display < y.display ? -1 : x.display > y.display ? 1 : 0))
      .map((g) => ({
        value: g.display,
        candidates: idsOf(g.mask),
        child: buildTree(g.mask),
      }));
    return {
      kind: "node",
      candidates: idsOf(mask),
      attribute: entry.attribute,
      askCost: costs[entry.attribute],
      branches,
    };
  }

  return { worstCaseCost, tree: buildTree(fullMask), statesExplored: memo.size };
}

/** 沿树模拟：返回每条根到叶子路径的询问成本之和，以及各叶子的候选集。 */
export function simulate(tree) {
  const out = [];
  function walk(t, acc) {
    if (t.kind === "leaf") {
      out.push({ pathCost: acc, leaf: t.candidates });
      return;
    }
    for (const b of t.branches) walk(b.child, acc + t.askCost);
  }
  walk(tree, 0);
  return out;
}

/** 树的文本可视化：每个节点/分支都标注候选集。 */
export function formatTree(solution) {
  const lines = [];
  lines.push(`最坏总成本 = ${solution.worstCaseCost}（搜索子集状态数 = ${solution.statesExplored}）`);
  function walk(t, indent) {
    if (t.kind === "leaf") {
      const tag = t.candidates.length > 1 ? "（不可区分等价组）" : "";
      lines.push(`${indent}叶 ${tag}{ ${t.candidates.join(", ")} }`);
      return;
    }
    lines.push(
      `${indent}{ ${t.candidates.join(", ")} } 询问 "${t.attribute}"（成本 ${t.askCost}）`,
    );
    for (const b of t.branches) {
      lines.push(`${indent}  ├─ ${t.attribute} = ${b.value} → { ${b.candidates.join(", ")} }`);
      walk(b.child, indent + "  │   ");
    }
  }
  walk(solution.tree, "");
  return lines.join("\n");
}
