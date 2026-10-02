import { Certificate } from "../src/certificate.ts";
import { Scheduler } from "../src/scheduler.ts";
import { formatInterval } from "../src/interval.ts";

const scheduler = new Scheduler();

// 员工 emp-1 考取焊接证书：有效期 [10, 100)
scheduler.addCertificate(
  new Certificate({ id: "cert-weld-1", employeeId: "emp-1", skill: "welding", validFrom: 10, validTo: 100 }),
);

console.log("== 场景 1：排班覆盖 [80, 120)，检测缺口并拒绝 ==");
const rejected = scheduler.schedule({
  id: "shift-night",
  employeeId: "emp-1",
  requiredSkill: "welding",
  span: { start: 80, end: 120 },
});
if (!rejected.ok) {
  console.log(`拒绝排班 shift-night，缺资质区间：${rejected.uncovered.map(formatInterval).join(", ")}`);
}

console.log("\n== 场景 2：班次 [50, 80) 全程有资质，排班成功 ==");
const accepted = scheduler.schedule({
  id: "shift-day",
  employeeId: "emp-1",
  requiredSkill: "welding",
  span: { start: 50, end: 80 },
});
console.log(`排班结果：${accepted.ok ? "接受" : "拒绝"}`);

console.log("\n== 场景 3：事务时间 30 收到追溯通知——证书在有效时间 60 被吊销 ==");
const warnings = scheduler.revoke("cert-weld-1", 60, 30);
for (const w of warnings) {
  console.log(
    `预警：班次 ${w.shiftId}（员工 ${w.employeeId} / 技能 ${w.requiredSkill}）` +
      `因证书 ${w.certificateId} 于 ${w.revokedAt} 被追溯吊销，` +
      `违规区间：${w.violated.map(formatInterval).join(", ")}`,
  );
}
if (warnings.length === 0) console.log("无受影响班次");
