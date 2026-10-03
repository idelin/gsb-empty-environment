import { QualificationMatcher } from "../src/matcher.ts";

const matcher = new QualificationMatcher();

// 员工 emp-1 考取叉车证书，生效期 [10, 100)
matcher.issueCertificate({
  id: "cert-1",
  employeeId: "emp-1",
  skill: "forklift",
  validFrom: 10,
  validTo: 100,
});

// 场景 1：排班 [80, 120)，检测到 [100, 120) 缺资质，拒绝排班
const rejected = matcher.scheduleShift(
  { id: "S1", employeeId: "emp-1", skill: "forklift", start: 80, end: 120 },
  0,
);
console.log("排班 S1 [80,120):", rejected.ok ? "通过" : "拒绝", "缺口:", rejected.missing);

// 场景 2：排班 [80, 95)，资质持续有效，排班成功
const accepted = matcher.scheduleShift(
  { id: "S2", employeeId: "emp-1", skill: "forklift", start: 80, end: 95 },
  0,
);
console.log("排班 S2 [80,95):", accepted.ok ? "通过" : "拒绝");

// 场景 3：知悉时间 200 收到通知——证书在 valid time 60 被追溯吊销
const report = matcher.recordRevocation("cert-1", 60, 200);
console.log(`\n吊销通知: 证书 ${report.certId} 于 ${report.revokedAt} 吊销（知悉时间 ${report.knownAt}）`);
for (const violation of report.violations) {
  console.log(
    `预警: 班次 ${violation.shift.id} [${violation.shift.start},${violation.shift.end}) 违规，`,
    "缺资质区间:",
    violation.missing,
  );
}

// 场景 4：复审延期至 150 后（知悉时间 210），[80,120) 仍因吊销截断被拒
matcher.recordReview("cert-1", 150, 210);
const afterReview = matcher.validateShift(
  { id: "S3", employeeId: "emp-1", skill: "forklift", start: 80, end: 120 },
  210,
);
console.log("\n复审延期至 150 后排班 [80,120):", afterReview.ok ? "通过" : "拒绝", "缺口:", afterReview.missing);
