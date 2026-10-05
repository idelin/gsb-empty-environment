// 人类可读的逐步证据报告。所有输出顺序固定，可直接 diff 重放。
import type { CheckReport, Spec } from "./types.ts";

function fmtFd(lhs: string[], rhs: string[]): string {
  return `${lhs.join("")} -> ${rhs.join("")}`;
}

function fmtTableau(spec: Spec, tableau: string[][]): string[] {
  const header = ["行", ...spec.attributes];
  const rows = tableau.map((row, i) => [`R${i}`, ...row]);
  const widths = header.map((h, c) =>
    Math.max(h.length, ...rows.map((r) => r[c].length))
  );
  const line = (cells: string[]) =>
    cells.map((s, c) => s.padEnd(widths[c])).join("  ").trimEnd();
  return [line(header), ...rows.map(line)];
}

export function renderReport(report: CheckReport): string {
  const { spec, chase, preservation } = report;
  const out: string[] = [];

  out.push("=== 输入 ===");
  out.push(`属性集 (${spec.attributes.length}): ${spec.attributes.join(" ")}`);
  spec.fds.forEach((fd, i) => out.push(`F${i}: ${fmtFd(fd.lhs, fd.rhs)}`));
  spec.decomposition.forEach((comp, i) => out.push(`R${i}: ${comp.join("")}`));
  out.push("");

  out.push("=== 无损连接检查（tableau chase） ===");
  out.push(...fmtTableau(spec, chaseTableauInitial(spec)));
  out.push("");
  if (chase.merges.length === 0) {
    out.push("无可应用的合并，chase 立即终止。");
  } else {
    out.push("合并证据（逐步）：");
    for (const m of chase.merges) {
      out.push(
        `  步骤 ${m.step}: 应用 F${m.fdIndex} (${fmtFd(m.fd.lhs, m.fd.rhs)}) ` +
        `于行 (R${m.rowA}, R${m.rowB})，列 ${m.attribute}: ` +
        `${m.eliminated} => ${m.kept}`
      );
    }
  }
  out.push("");
  out.push("最终 tableau：");
  out.push(...fmtTableau(spec, chase.tableau));
  out.push("");
  if (chase.lossless) {
    out.push(
      `结论：无损连接。第 R${chase.witnessRow} 行已全为杰出变量 ` +
      `(${spec.attributes.map((_, k) => `a${k}`).join(" ")})。`
    );
  } else {
    out.push("结论：有损连接。chase 终止后不存在全杰出变量行。");
  }
  out.push("");

  out.push("=== 依赖保持检查（独立于无损性，二者互不蕴含） ===");
  for (const p of preservation) {
    const head = `F${p.fdIndex} (${fmtFd(p.fd.lhs, p.fd.rhs)}): `;
    if (p.preserved) {
      out.push(`${head}保持。`);
    } else {
      out.push(`${head}不保持。`);
      out.push(`  G 下闭包 (${p.fd.lhs.join("")})+_G = { ${p.reachableClosure.join(", ")} }`);
      out.push(`  F 下闭包 (${p.fd.lhs.join("")})+_F = { ${p.fullClosure.join(", ")} }`);
      out.push(`  闭包缺口: { ${p.closureGap.join(", ")} }`);
      out.push(`  无法推出的右部属性: { ${p.missingRhs.join(", ")} }`);
    }
  }
  out.push(
    report.dependenciesPreserved
      ? "结论：全部分解投影的并 G 蕴含 F，依赖保持。"
      : "结论：存在不被保持的依赖（见上方闭包缺口）。"
  );
  out.push("");

  out.push("=== 总结（两项判定相互独立） ===");
  out.push(`无损连接: ${chase.lossless ? "是" : "否"}`);
  out.push(`依赖保持: ${report.dependenciesPreserved ? "是" : "否"}`);
  return out.join("\n");
}

// 报告需要初始 tableau；从 chase 结果无法还原，故重新构造（纯函数，确定）。
import { initialTableau } from "./chase.ts";
function chaseTableauInitial(spec: Spec): string[][] {
  return initialTableau(spec);
}
