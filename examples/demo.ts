/**
 * 运行样例：复现验收场景，并展示汇报链与审计轨迹。
 * 运行：node examples/demo.ts （需 Node >= 23.6，或 22.6+ 加 --experimental-strip-types）
 */
import { OrgChart } from '../src/org-chart.ts';

const org = new OrgChart();

// 知悉时刻 1：录入 E 在业务 [1,10) 汇报给 M1；同时建立上级汇报线与部门归属
org.setManager('M1', 'D1', 0, 100, 1);
org.setManager('M2', 'D2', 0, 100, 1);
org.setDepartment('E', 'Dept-A', 1, 10, 1);
org.setManager('E', 'M1', 1, 10, 1);

// 知悉时刻 5：补录历史调岗 —— E 在业务 [3,7) 实际汇报给 M2
org.setManager('E', 'M2', 3, 7, 5);

console.log('== 双时态点查询（验收边界） ==');
console.log('知悉6 查业务4  =>', org.managerAt('E', 4, 6)); // M2
console.log('知悉6 查业务8  =>', org.managerAt('E', 8, 6)); // M1
console.log('知悉4 查业务4  =>', org.managerAt('E', 4, 4)); // M1

console.log('\n== 知悉6 视角下 E 的汇报线业务时间线（自动分裂） ==');
for (const r of org.managerTimelineAt('E', 6)) {
  console.log(`业务[${r.validFrom}, ${r.validTo}) 汇报给 ${r.value}`);
}

console.log('\n== 汇报链还原 ==');
console.log('知悉6 / 业务4 :', org.reportingChainAt('E', 4, 6).map((n) => n.employeeId).join(' -> '));
console.log('知悉4 / 业务4 :', org.reportingChainAt('E', 4, 4).map((n) => n.employeeId).join(' -> '));

console.log('\n== E 的汇报线审计轨迹（含被修订关闭的记录） ==');
for (const r of org.managerHistory('E')) {
  const sysTo = r.sysTo === Infinity ? '∞' : r.sysTo;
  console.log(`业务[${r.validFrom}, ${r.validTo}) 汇报给 ${r.value} ｜ 系统[${r.sysFrom}, ${sysTo})`);
}
