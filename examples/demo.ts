import { OrgChart, OPEN } from "../src/org-chart.ts";

const fmt = (t: number): string => (t === OPEN ? "∞" : String(t));

const org = new OrgChart();

console.log("== 录入历史 ==\n");
console.log("知悉 1：录入 E 在业务 [1,10) 汇报给 M1");
org.assignManager("E", "M1", 1, 10, 1);
org.assignManager("M1", "D1", 1, 10, 1);
org.assignManager("M2", "D2", 1, 10, 1);
console.log("知悉 5：补录 E 在业务 [3,7) 汇报给 M2（历史调岗）\n");
org.assignManager("E", "M2", 3, 7, 5);

console.log("== 双时态查询 ==\n");
const queries: Array<[number, number]> = [
  [4, 6],
  [8, 6],
  [4, 4],
];
for (const [businessTime, knowledgeTime] of queries) {
  const manager = org.managerAt("E", businessTime, knowledgeTime);
  const chain = org.reportingChain("E", businessTime, knowledgeTime);
  console.log(
    `知悉 ${knowledgeTime} 查询业务 ${businessTime}：直接上级 = ${manager}，` +
      `汇报链 = ${chain.join(" -> ")}`,
  );
}

console.log("\n== E 的汇报线审计轨迹（含已封闭的历史版本）==\n");
for (const r of org.reportingAuditTrail("E")) {
  const state = r.sysTo === OPEN ? "当前知悉" : `知悉 ${r.sysTo} 起被修订`;
  console.log(
    `业务 [${fmt(r.validFrom)}, ${fmt(r.validTo)}) 汇报给 ${r.value}，` +
      `录入于知悉 ${r.sysFrom}（${state}）`,
  );
}
