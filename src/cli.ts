import { readFileSync } from "node:fs";
import type { AttrDef, Policy } from "./model.ts";
import { comparePolicies, validateAttrs, type Region } from "./compare.ts";

function loadJson<T>(path: string): T {
  return JSON.parse(readFileSync(path, "utf8")) as T;
}

const KIND_LABEL: Record<Region["kind"], string> = {
  "allow-to-deny": "allow → deny（判定收紧）",
  "deny-to-allow": "deny → allow（判定放宽）",
  "explanation-only": "仅解释改变（判定不变）",
};

function printRegion(region: Region): void {
  console.log(`\n### ${KIND_LABEL[region.kind]} — 共 ${region.count} 个输入`);
  console.log("覆盖摘要（按属性投影，可能为超集）:");
  for (const c of region.coverage) {
    console.log(`  - ${c.attr}: ${c.summary}`);
  }
  console.log("代表样例:");
  for (const s of region.samples) {
    console.log(`  - 输入 ${JSON.stringify(s.input)}`);
    console.log(`    旧: ${s.oldDecision} | ${s.oldReasons.join("; ") || "(无命中规则)"}`);
    console.log(`    新: ${s.newDecision} | ${s.newReasons.join("; ") || "(无命中规则)"}`);
  }
}

function main(): void {
  const [attrsPath, oldPath, newPath] = process.argv.slice(2);
  if (!attrsPath || !oldPath || !newPath) {
    console.error("用法: node src/cli.ts <attrs.json> <old-policy.json> <new-policy.json>");
    process.exit(2);
  }
  const attrs = loadJson<AttrDef[]>(attrsPath);
  const oldPolicy = loadJson<Policy>(oldPath);
  const newPolicy = loadJson<Policy>(newPath);
  validateAttrs(attrs);

  const report = comparePolicies(attrs, oldPolicy, newPolicy);

  console.log(`对比: ${oldPolicy.name}  ->  ${newPolicy.name}`);
  console.log("\n== 有限分析范围（仅在此范围内穷举，域外输入未覆盖）==");
  for (const s of report.scope) {
    console.log(`  - ${s.attr}: ${s.summary}`);
  }
  console.log(`  合计 ${report.totalInputs} 个输入组合`);

  console.log("\n== 统计 ==");
  console.log(`  判定改变 allow→deny: ${report.counts["allow-to-deny"]}`);
  console.log(`  判定改变 deny→allow: ${report.counts["deny-to-allow"]}`);
  console.log(`  仅解释改变:          ${report.counts["explanation-only"]}`);
  console.log(`  完全不变:            ${report.counts["unchanged"]}`);

  for (const region of report.regions) {
    printRegion(region);
  }
  if (report.regions.length === 0) {
    console.log("\n两规则集在分析范围内语义完全等价（判定与解释均一致）。");
  }
}

main();
