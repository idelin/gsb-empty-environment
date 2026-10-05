import { readFileSync } from "node:fs";
import type { Constraint } from "./types.ts";
import { formatConstraint } from "./types.ts";
import { solve, MAX_FEATURES, configMask, type SolveResult } from "./solver.ts";
import { explainUnreachable } from "./explain.ts";

interface RawInput {
  features?: unknown;
  constraints?: unknown;
}

function fail(msg: string): never {
  console.error(`错误：${msg}`);
  process.exit(1);
}

function parseInput(path: string): { names: string[]; constraints: Constraint[] } {
  let raw: RawInput;
  try {
    raw = JSON.parse(readFileSync(path, "utf8")) as RawInput;
  } catch (e) {
    fail(`无法读取或解析输入文件 ${path}：${(e as Error).message}`);
  }
  if (!Array.isArray(raw.features)) fail("输入必须包含 features 数组");
  const names = raw.features.map((x) => {
    if (typeof x !== "string" || x.length === 0) fail("features 必须是非空字符串");
    return x as string;
  });
  if (names.length < 1 || names.length > MAX_FEATURES) {
    fail(`特性数量须在 1..${MAX_FEATURES} 之间，当前为 ${names.length}`);
  }
  if (new Set(names).size !== names.length) fail("特性名必须唯一");
  const index = new Map(names.map((nm, i) => [nm, i]));
  const ref = (x: unknown): number => {
    if (typeof x !== "string" || !index.has(x)) fail(`约束引用了未知特性：${String(x)}`);
    return index.get(x)!;
  };
  const constraints: Constraint[] = [];
  const seen = new Set<string>();
  for (const c of Array.isArray(raw.constraints) ? raw.constraints : []) {
    const o = c as Record<string, unknown>;
    if (o.type === "mutex") {
      const a = ref(o.a);
      const b = ref(o.b);
      if (a === b) fail("mutex 约束的两个特性不能相同");
      const key = `mutex:${Math.min(a, b)}:${Math.max(a, b)}`;
      if (!seen.has(key)) {
        seen.add(key);
        constraints.push({ type: "mutex", a, b });
      }
    } else if (o.type === "implies") {
      const from = ref(o.from);
      const to = ref(o.to);
      if (from === to) fail("implies 约束的两个特性不能相同");
      const key = `implies:${from}:${to}`;
      if (!seen.has(key)) {
        seen.add(key);
        constraints.push({ type: "implies", from, to });
      }
    } else {
      fail(`未知约束类型：${String(o.type)}（支持 mutex / implies）`);
    }
  }
  return { names, constraints };
}

function formatConfig(config: number, names: string[]): string {
  return names.map((nm, i) => `${nm}=${(config >> i) & 1}`).join(" ");
}

function printReport(
  names: string[],
  constraints: Constraint[],
  result: SolveResult,
): void {
  const n = names.length;
  const totalPairs = (n * (n - 1)) / 2;
  const lines: string[] = [];
  lines.push(`特性数：${n}    约束数：${constraints.length}`);
  lines.push(`合法配置：${result.validConfigs.length} / ${1 << n}`);
  lines.push(`可达两两组合：${result.reachableCount} / ${totalPairs * 4}`);
  lines.push("");
  lines.push(`== 最小测试配置集：${result.minCount} 个 ==`);
  result.configs.forEach((c, idx) => {
    lines.push(`  #${idx + 1}  ${formatConfig(c, names)}`);
  });
  lines.push("");
  lines.push("== 最小性证明 ==");
  const bd = result.boundDetails;
  const st = result.structural;
  lines.push(
    `下界：max(特性对界限 ${bd.pairBound}, 总量界限 ${bd.totalBound}, 结构界限 ${st.bound}` +
      `${result.lp ? `, LP 界限 ${result.lp.bound}` : ""}) = ${result.lowerBound}`,
  );
  if (bd.pairBoundPair) {
    const [pi, pj] = bd.pairBoundPair;
    lines.push(
      `  · 特性对界限：任一配置对同一特性对至多覆盖 1 种取值组合；` +
        `特性对 (${names[pi]}, ${names[pj]}) 有 ${bd.pairBound} 个可达组合 ⇒ 至少需要 ${bd.pairBound} 个配置`,
    );
  }
  lines.push(
    `  · 总量界限：可达组合共 ${result.reachableCount} 个，单配置至多覆盖 ${totalPairs} 个 ` +
      `⇒ 至少需要 ⌈${result.reachableCount}/${totalPairs}⌉ = ${bd.totalBound} 个配置`,
  );
  if (result.lp) {
    lines.push(
      `  · LP 界限：集合覆盖 LP 松弛的对偶打包值 = ${result.lp.value.toFixed(3)}` +
        `（整数负载精确验证的对偶可行解）⇒ 至少需要 ${result.lp.bound} 个配置`,
    );
  }
  if (st.bound > 0 && st.subset.length > 0) {
    lines.push(
      `  · 结构界限：特性子集 { ${st.subset.map((i) => names[i]).join(", ")} }（${st.subset.length} 个）` +
        `内部两两组合全部可达；k 个配置在该子集上构成二元 2-覆盖族，其大小至多为 maxk(k)。` +
        `经精确最大团计算 maxk(${st.bound - 1}) = ${st.maxkBelow} < ${st.subset.length}` +
        ` ⇒ 至少需要 ${st.bound} 个配置`,
    );
  }
  const failedTargets = result.stats.filter((s) => !s.solved);
  if (result.minCount === result.lowerBound && failedTargets.length === 0) {
    lines.push(`所得配置数 ${result.minCount} 等于下界，直接得证最小（无需搜索）。`);
  } else if (result.minCount === result.lowerBound) {
    lines.push(`所得配置数 ${result.minCount} 等于下界，直接得证最小。`);
  }
  if (failedTargets.length > 0) {
    for (const s of failedTargets) {
      lines.push(
        `目标 k=${s.target}：穷尽分支限界搜索失败（节点 ${s.nodes}，界限剪枝 ${s.prunedByBound}，` +
          `记忆命中 ${s.memoHits}，失败状态 ${s.failedStates}）⇒ 不存在 ${s.target} 个配置的覆盖`,
      );
    }
    lines.push(`所有小于 ${result.minCount} 的目标均已穷尽搜索并排除，故 ${result.minCount} 最小。`);
  }
  lines.push("");
  lines.push("== 覆盖校验 ==");
  lines.push(
    `可达组合覆盖：${result.reachableCount}/${result.reachableCount}（100%）` +
      `；非法配置使用数：0（构造保证，已逐项复核：${result.verified ? "通过" : "失败！"}）`,
  );

  const unreachable = explainUnreachable(n, constraints, result.reachable, names);
  lines.push("");
  lines.push(`== 不可达两两组合：${unreachable.length} 个（不计入覆盖率）==`);
  for (const u of unreachable) {
    lines.push(`  · ${names[u.i]}=${u.vi}, ${names[u.j]}=${u.vj}：${u.explanation}`);
  }
  console.log(lines.join("\n"));
}

function main(): void {
  const args = process.argv.slice(2);
  const jsonMode = args.includes("--json");
  const path = args.find((a) => !a.startsWith("--"));
  if (!path) {
    console.error("用法：node src/cli.ts <input.json> [--json]");
    process.exit(1);
  }
  const { names, constraints } = parseInput(path);
  const result = solve(names.length, constraints);

  if (jsonMode) {
    const unreachable = explainUnreachable(names.length, constraints, result.reachable, names);
    console.log(
      JSON.stringify(
        {
          features: names,
          constraints: constraints.map((c) => formatConstraint(c, names)),
          validConfigCount: result.validConfigs.length,
          reachablePairCount: result.reachableCount,
          minCount: result.minCount,
          configs: result.configs.map((c) => formatConfig(c, names)),
          lowerBound: result.lowerBound,
          proof: {
            lowerBound: result.lowerBound,
            failedTargets: result.stats
              .filter((s) => !s.solved)
              .map((s) => ({ target: s.target, nodes: s.nodes })),
          },
          verified: result.verified,
          unreachable: unreachable.map((u) => ({
            combo: `${names[u.i]}=${u.vi}, ${names[u.j]}=${u.vj}`,
            kind: u.kind,
            explanation: u.explanation,
          })),
        },
        null,
        2,
      ),
    );
    return;
  }
  printReport(names, constraints, result);
}

main();
