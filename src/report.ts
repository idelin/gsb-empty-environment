/** 把审计结果格式化为人类可读的报告 */

import type { AuditResult, Counterexample } from "./audit.ts";
import type { Assignment, BuildSpec, InputVar } from "./model.ts";

const KIND_LABEL: Record<InputVar["kind"], string> = {
  env: "环境变量",
  tool: "工具版本",
  "conditional-read": "条件读取",
};

function formatAssignment(a: Assignment, vars: readonly string[]): string {
  return vars.map((v) => `${v}=${a[v]}`).join(", ");
}

function formatCounterexample(
  spec: BuildSpec,
  cex: Counterexample,
  index: number,
): string[] {
  const inputOf = new Map(spec.inputs.map((i) => [i.name, i]));
  const lines: string[] = [];
  const varDesc = cex.vars
    .map((v) => {
      const input = inputOf.get(v);
      return input ? `${v}（${KIND_LABEL[input.kind]}）` : v;
    })
    .join(" + ");
  lines.push(`${index}. 差异变量: ${varDesc}`);

  const allDeclared = cex.vars.every((v) => {
    const probe = { ...cex.a, [v]: "__never__" };
    return spec.key(probe) !== spec.key(cex.a);
  });
  if (cex.vars.length > 1 && allDeclared) {
    lines.push(
      "   性质: 键编码有损 —— 这些变量均已声明，但联合变化相互抵消，键保持不变",
    );
  } else {
    const undeclared = cex.vars.filter((v) => {
      const probe = { ...cex.a, [v]: "__never__" };
      return spec.key(probe) === spec.key(cex.a);
    });
    if (undeclared.length > 0) {
      lines.push(`   性质: 未声明输入 —— ${undeclared.join(", ")} 不在缓存键中`);
    }
  }

  for (const v of cex.vars) {
    const input = inputOf.get(v);
    if (input?.readWhen) {
      lines.push(
        `   条件读取: ${v} 仅在满足条件时被读取（A 侧: ${input.readWhen(cex.a) ? "读取" : "不读取"}，B 侧: ${input.readWhen(cex.b) ? "读取" : "不读取"}）`,
      );
    }
  }

  lines.push(`   缓存键（两侧相同）: ${cex.key}`);
  lines.push(`   A: ${formatAssignment(cex.a, cex.vars)}`);
  lines.push(`     产物: ${cex.outputA.artifact}`);
  lines.push(`     日志: ${JSON.stringify(cex.outputA.logs)}`);
  lines.push(`   B: ${formatAssignment(cex.b, cex.vars)}`);
  lines.push(`     产物: ${cex.outputB.artifact}`);
  lines.push(`     日志: ${JSON.stringify(cex.outputB.logs)}`);
  return lines;
}

export function formatReport(spec: BuildSpec, result: AuditResult): string {
  const lines: string[] = [];
  lines.push(`# 缓存键审计报告: ${result.buildName}`);
  lines.push("");
  lines.push(
    `输入空间: ${result.totalAssignments} 个赋值组合 → ${result.distinctKeys} 个不同缓存键`,
  );
  lines.push("");
  lines.push("## 声明 vs 实际使用");
  lines.push(`- 键中声明的输入: ${result.declared.join(", ") || "（无）"}`);
  lines.push(`- 实际影响输出的输入: ${result.used.join(", ") || "（无）"}`);
  lines.push(
    `- ⚠ 使用了却未声明（可致缓存错误命中）: ${result.undeclaredButUsed.join(", ") || "（无）"}`,
  );
  lines.push(
    `- 声明了却未使用（键冗余）: ${result.declaredButUnused.join(", ") || "（无）"}`,
  );
  lines.push("");

  const artifactCex = result.counterexamples.filter((c) => c.kind === "artifact");
  const logOnlyCex = result.counterexamples.filter((c) => c.kind === "log-only");
  lines.push(
    `## 最小反例（共 ${result.counterexamples.length} 个：${artifactCex.length} 个影响产物，${logOnlyCex.length} 个仅影响日志）`,
  );
  lines.push("");

  if (result.counterexamples.length === 0) {
    lines.push("未发现反例：在枚举范围内，键相同的输入对总是产生相同输出。");
    return lines.join("\n");
  }

  lines.push("### 影响产物（缓存命中会返回错误产物）");
  lines.push("");
  if (artifactCex.length === 0) lines.push("（无）");
  artifactCex.forEach((cex, i) => {
    lines.push(...formatCounterexample(spec, cex, i + 1));
    lines.push("");
  });

  lines.push("### 仅影响日志（产物一致，仅日志不同）");
  lines.push("");
  if (logOnlyCex.length === 0) lines.push("（无）");
  logOnlyCex.forEach((cex, i) => {
    lines.push(...formatCounterexample(spec, cex, i + 1));
    lines.push("");
  });

  lines.push("## 修复建议");
  if (result.undeclaredButUsed.length > 0) {
    lines.push(
      `- 将以下输入纳入缓存键（或消除其对输出的影响）: ${result.undeclaredButUsed.join(", ")}`,
    );
  }
  const lossy = artifactCex.filter((c) =>
    c.vars.every((v) => result.declared.includes(v) && c.vars.length > 1),
  );
  if (lossy.length > 0) {
    lines.push(
      `- 键编码有损：${lossy.map((c) => c.vars.join(" + ")).join("；")} 的组合在键中被折叠，请编码完整取值`,
    );
  }
  if (logOnlyCex.length > 0) {
    lines.push(
      "- 仅影响日志的输入可选择不进键，但需确认下游不依赖日志一致性；否则同样纳入键中",
    );
  }
  return lines.join("\n");
}
