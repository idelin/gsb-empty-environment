import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OrgChart } from '../src/org-chart.ts';
import { BitemporalTable } from '../src/bitemporal.ts';

/**
 * 验收场景：
 *  - 知悉1 录入：员工 E 业务[1,10) 汇报给 M1
 *  - 知悉5 补录：员工 E 业务[3,7) 汇报给 M2
 *  - 知悉6 查业务4 => M2；知悉6 查业务8 => M1；知悉4 查业务4 => M1
 */
function buildAcceptanceChart(): OrgChart {
  const org = new OrgChart();
  org.setManager('E', 'M1', 1, 10, 1);
  org.setManager('E', 'M2', 3, 7, 5);
  return org;
}

test('验收：知悉6 查询业务4，汇报给 M2', () => {
  assert.equal(buildAcceptanceChart().managerAt('E', 4, 6), 'M2');
});

test('验收：知悉6 查询业务8，汇报给 M1', () => {
  assert.equal(buildAcceptanceChart().managerAt('E', 8, 6), 'M1');
});

test('验收：知悉4 查询业务4，汇报给 M1（补录前的历史认知）', () => {
  assert.equal(buildAcceptanceChart().managerAt('E', 4, 4), 'M1');
});

test('补录自动分裂重叠任职时间段，审计轨迹完整', () => {
  const history = buildAcceptanceChart().managerHistory('E');
  assert.deepEqual(
    history.map((r) => [r.value, r.validFrom, r.validTo, r.sysFrom, r.sysTo]),
    [
      ['M1', 1, 10, 1, 5],        // 原始记录：知悉5 起被关闭
      ['M1', 1, 3, 5, Infinity],  // 分裂残余：左段
      ['M2', 3, 7, 5, Infinity],  // 补录记录
      ['M1', 7, 10, 5, Infinity], // 分裂残余：右段
    ],
  );
});

test('知悉6 的业务时间线被分裂为三段', () => {
  const timeline = buildAcceptanceChart().managerTimelineAt('E', 6);
  assert.deepEqual(
    timeline.map((r) => [r.value, r.validFrom, r.validTo]),
    [['M1', 1, 3], ['M2', 3, 7], ['M1', 7, 10]],
  );
});

test('半开区间边界：validTo 不包含，validFrom 包含', () => {
  const org = buildAcceptanceChart();
  assert.equal(org.managerAt('E', 3, 6), 'M2');  // 业务3 起属 M2
  assert.equal(org.managerAt('E', 7, 6), 'M1');  // 业务7 起回到 M1
  assert.equal(org.managerAt('E', 10, 6), undefined); // 业务10 已超出 [1,10)
});

test('历史任意时刻汇报链：沿汇报线逐级还原', () => {
  const org = new OrgChart();
  org.setManager('M1', 'D1', 0, 100, 1);   // M1 一直汇报给总监 D1
  org.setManager('M2', 'D2', 0, 100, 1);   // M2 一直汇报给总监 D2
  org.setDepartment('E', 'Dept-A', 1, 10, 1);
  org.setManager('E', 'M1', 1, 10, 1);
  org.setManager('E', 'M2', 3, 7, 5);

  const chainNow = org.reportingChainAt('E', 4, 6);
  assert.deepEqual(chainNow.map((n) => n.employeeId), ['E', 'M2', 'D2']);
  assert.equal(chainNow[0].departmentId, 'Dept-A');

  const chainThen = org.reportingChainAt('E', 4, 4);
  assert.deepEqual(chainThen.map((n) => n.employeeId), ['E', 'M1', 'D1']);
});

test('部门归属同样支持补录分裂与历史回溯', () => {
  const org = new OrgChart();
  org.setDepartment('E', 'Dept-A', 1, 10, 1);
  org.setDepartment('E', 'Dept-B', 5, 8, 3); // 知悉3 补录调岗

  assert.equal(org.departmentAt('E', 6, 4), 'Dept-B');
  assert.equal(org.departmentAt('E', 6, 2), 'Dept-A');
  assert.equal(org.departmentAt('E', 9, 4), 'Dept-A');
});

test('不重叠的录入不触发分裂', () => {
  const org = new OrgChart();
  org.setManager('E', 'M1', 1, 5, 1);
  org.setManager('E', 'M2', 5, 9, 2); // 与 [1,5) 首尾相接，不重叠
  assert.equal(org.managerHistory('E').length, 2);
  assert.equal(org.managerAt('E', 4, 3), 'M1');
  assert.equal(org.managerAt('E', 5, 3), 'M2');
});

test('汇报链环检测：脏数据不致死循环', () => {
  const org = new OrgChart();
  org.setManager('A', 'B', 0, 10, 1);
  org.setManager('B', 'A', 0, 10, 1);
  assert.deepEqual(org.reportingChainAt('A', 5, 5).map((n) => n.employeeId), ['A', 'B']);
});

test('非法输入：空业务区间、系统时间倒流、自我汇报', () => {
  const org = new OrgChart();
  assert.throws(() => org.setManager('E', 'M1', 5, 5, 1), RangeError);
  assert.throws(() => org.setManager('E', 'E', 1, 2, 1), RangeError);
  org.setManager('E', 'M1', 1, 2, 3);
  assert.throws(() => org.setManager('E', 'M2', 1, 2, 2), RangeError);
});

test('BitemporalTable：无记录时返回 undefined', () => {
  const table = new BitemporalTable<string, string>();
  assert.equal(table.valueAt('nobody', 0, 0), undefined);
  assert.deepEqual(table.history('nobody'), []);
});
