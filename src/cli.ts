import { readFileSync } from 'fs';
import { Problem } from './types';
import { schedule } from './scheduler';
import { RULE_DESCRIPTIONS } from './solver';
import { formatMinutes } from './time';

const DAY_NAMES = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];

function formatRange(start: number, end: number): string {
  const suffix = end <= start ? '(+1)' : '';
  return `${formatMinutes(start)}-${formatMinutes(end)}${suffix}`;
}

function main(): void {
  const args = process.argv.slice(2);
  if (args.length === 0) {
    console.error('用法: weekly-scheduler <config.json> [搜索节点预算]');
    process.exit(2);
  }
  const configPath = args[0];
  const budget = args.length > 1 ? Number(args[1]) : undefined;

  let problem: Problem;
  try {
    problem = JSON.parse(readFileSync(configPath, 'utf8')) as Problem;
  } catch (err) {
    console.error(`无法读取配置文件 ${configPath}: ${String(err)}`);
    process.exit(2);
  }

  const result = schedule(problem, budget);

  if (result.status === 'invalid') {
    console.error('配置不合法：');
    for (const e of result.errors ?? []) console.error(`  - ${e.message}`);
    process.exit(2);
  }

  if (result.status === 'infeasible' || result.status === 'unknown') {
    console.log(result.status === 'infeasible' ? '无法排班：约束集合不可满足。' : '搜索超出预算，无法判定。');
    const core = result.core;
    if (core) {
      console.log('');
      console.log('导致无解的最小班次与规则集合：');
      console.log('  班次：');
      for (const s of core.shifts) {
        console.log(
          `    - ${s.id}（第${s.day + 1}天 ${DAY_NAMES[s.day]} ${formatRange(s.start, s.end)}，技能: ${s.skill}，需求 ${s.required ?? 1} 人）`,
        );
      }
      console.log('  规则：');
      for (const r of core.rules) console.log(`    - ${RULE_DESCRIPTIONS[r]}`);
      console.log(`  涉及员工：${core.employeeIds.length > 0 ? core.employeeIds.join('、') : '（无匹配员工）'}`);
      console.log('');
      console.log('说明：移除上述任一班次，或放宽上述任一规则后，问题恢复可解。');
    }
    process.exit(1);
  }

  const optimalLabel = result.optimal ? '最优' : '可行（超出预算，未证明最优）';
  console.log(`排班结果（${optimalLabel}，总偏好损失 = ${result.totalPreferenceCost}，搜索节点 ${result.nodes}）`);
  for (let d = 0; d < 7; d++) {
    const dayEntries = result.schedule.filter((e) => e.day === d);
    if (dayEntries.length === 0) continue;
    console.log(`第${d + 1}天 ${DAY_NAMES[d]}`);
    for (const e of dayEntries) {
      const pref = e.preferenceCost > 0 ? `（偏好损失 ${e.preferenceCost}）` : '';
      console.log(`  ${e.shiftId} ${formatRange(e.start, e.end)} -> ${e.employeeId}${pref}`);
    }
  }
  console.log('');
  console.log('员工工时统计：');
  for (const st of result.stats) {
    const hours = (st.totalMinutes / 60).toFixed(1);
    const pref = st.preferenceCost > 0 ? `，偏好损失 ${st.preferenceCost}` : '';
    console.log(`  ${st.employeeId}: ${st.shiftCount} 班，共 ${hours} 小时${pref}`);
  }
}

main();
