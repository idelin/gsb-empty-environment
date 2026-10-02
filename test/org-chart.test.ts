import { test } from "node:test";
import assert from "node:assert/strict";
import { OrgChart, OPEN } from "../src/org-chart.ts";

test("验收边界：补录历史调岗后，不同知悉时刻看到不同历史", () => {
  const org = new OrgChart();

  // 知悉 1 录入：员工 E 在业务 [1,10) 汇报给 M1
  org.assignManager("E", "M1", 1, 10, 1);
  // 知悉 5 补录：员工 E 在业务 [3,7) 汇报给 M2
  org.assignManager("E", "M2", 3, 7, 5);

  // 知悉 6 查询业务 4 -> M2（补录已生效）
  assert.equal(org.managerAt("E", 4, 6), "M2");
  // 知悉 6 查询业务 8 -> M1（补录区间之外仍是 M1）
  assert.equal(org.managerAt("E", 8, 6), "M1");
  // 知悉 4 查询业务 4 -> M1（补录尚未录入，历史视图不变）
  assert.equal(org.managerAt("E", 4, 4), "M1");
});

test("补录时自动分裂重叠的任职时间段", () => {
  const org = new OrgChart();
  org.assignManager("E", "M1", 1, 10, 1);
  org.assignManager("E", "M2", 3, 7, 5);

  const trail = org.reportingAuditTrail("E");

  // 原始记录 [1,10)->M1 在知悉 5 被封闭
  assert.deepEqual(
    trail.find((r) => r.validFrom === 1 && r.validTo === 10),
    {
      subject: "E",
      value: "M1",
      validFrom: 1,
      validTo: 10,
      sysFrom: 1,
      sysTo: 5,
    },
  );

  // 当前仍被知悉的三段：前段 [1,3)->M1、补录段 [3,7)->M2、后段 [7,10)->M1
  const current = trail.filter((r) => r.sysTo === OPEN);
  assert.deepEqual(
    current.map((r) => [r.value, r.validFrom, r.validTo, r.sysFrom]),
    [
      ["M1", 1, 3, 5],
      ["M2", 3, 7, 5],
      ["M1", 7, 10, 5],
    ],
  );
});

test("业务时间区间左闭右开", () => {
  const org = new OrgChart();
  org.assignManager("E", "M1", 1, 10, 1);

  assert.equal(org.managerAt("E", 1, 1), "M1");
  assert.equal(org.managerAt("E", 9.999, 1), "M1");
  assert.equal(org.managerAt("E", 10, 1), undefined);
  assert.equal(org.managerAt("E", 0.999, 1), undefined);
});

test("完全覆盖旧时间段时不产生分裂残段", () => {
  const org = new OrgChart();
  org.assignManager("E", "M1", 1, 10, 1);
  org.assignManager("E", "M2", 0, 12, 3);

  const current = org.reportingAuditTrail("E").filter((r) => r.sysTo === OPEN);
  assert.deepEqual(
    current.map((r) => [r.value, r.validFrom, r.validTo]),
    [["M2", 0, 12]],
  );
  assert.equal(org.managerAt("E", 5, 3), "M2");
  assert.equal(org.managerAt("E", 5, 2), "M1");
});

test("历史任意时刻的汇报链查询", () => {
  const org = new OrgChart();
  org.assignManager("E", "M1", 1, 10, 1);
  org.assignManager("M1", "D1", 1, 10, 1);
  org.assignManager("M2", "D2", 1, 10, 1);
  org.assignManager("E", "M2", 3, 7, 5);

  // 知悉 6、业务 4：E -> M2 -> D2
  assert.deepEqual(org.reportingChain("E", 4, 6), ["E", "M2", "D2"]);
  // 知悉 6、业务 8：E -> M1 -> D1
  assert.deepEqual(org.reportingChain("E", 8, 6), ["E", "M1", "D1"]);
  // 知悉 4、业务 4：补录未发生，E -> M1 -> D1
  assert.deepEqual(org.reportingChain("E", 4, 4), ["E", "M1", "D1"]);
  // 无上级时链即本人
  assert.deepEqual(org.reportingChain("D1", 4, 6), ["D1"]);
});

test("部门归属同样支持双时态与追溯修订", () => {
  const org = new OrgChart();
  org.assignDepartment("E", "Dept-A", 1, 10, 1);
  org.assignDepartment("E", "Dept-B", 4, 6, 5);

  assert.equal(org.departmentAt("E", 5, 6), "Dept-B");
  assert.equal(org.departmentAt("E", 7, 6), "Dept-A");
  assert.equal(org.departmentAt("E", 5, 4), "Dept-A");

  const current = org
    .departmentAuditTrail("E")
    .filter((r) => r.sysTo === OPEN);
  assert.deepEqual(
    current.map((r) => [r.value, r.validFrom, r.validTo]),
    [
      ["Dept-A", 1, 4],
      ["Dept-B", 4, 6],
      ["Dept-A", 6, 10],
    ],
  );
});

test("开放时间段（validTo = OPEN）可被后续补录截断", () => {
  const org = new OrgChart();
  org.assignManager("E", "M1", 1, OPEN, 1);
  org.assignManager("E", "M2", 5, 8, 3);

  assert.equal(org.managerAt("E", 6, 3), "M2");
  assert.equal(org.managerAt("E", 100, 3), "M1");
  assert.equal(org.managerAt("E", 2, 3), "M1");
});

test("非法时间段与自汇报被拒绝", () => {
  const org = new OrgChart();
  assert.throws(() => org.assignManager("E", "M1", 5, 5, 1), RangeError);
  assert.throws(() => org.assignManager("E", "M1", 7, 5, 1), RangeError);
  assert.throws(() => org.assignManager("E", "E", 1, 2, 1), RangeError);
});

test("汇报环检测", () => {
  const org = new OrgChart();
  org.assignManager("A", "B", 1, 10, 1);
  org.assignManager("B", "A", 1, 10, 1);
  assert.throws(() => org.reportingChain("A", 5, 1), /汇报环/);
});
