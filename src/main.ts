import { catalog, makeTerms, requiredCourses } from './catalog.ts';
import { planCourses } from './planner.ts';
import type { CycleReport, Plan } from './types.ts';

const SEASON_LABEL: Record<string, string> = {
  spring: '春',
  summer: '夏',
  fall: '秋',
  winter: '冬',
};

function printCycles(cycles: CycleReport[]): void {
  if (cycles.length === 0) {
    console.log('先修环检测：未发现无法通过同修解决的先修环。');
    return;
  }
  console.log(`先修环检测：发现 ${cycles.length} 个无法通过同修解决的先修环：`);
  for (const cycle of cycles) {
    console.log(`  环: ${cycle.courses.join(' -> ')}`);
    for (const e of cycle.edges) {
      console.log(`    ${e.from} 的先修要求#${e.requirementIndex + 1} 依赖 ${e.to}（严格先修，同修无法解开）`);
    }
  }
}

function printPlan(plan: Plan): void {
  console.log('\n===== 最早毕业方案 =====');
  for (const term of plan.terms) {
    const label = `${term.spec.year} ${SEASON_LABEL[term.spec.season]}`;
    if (term.courses.length === 0) {
      console.log(`\n${label}（空学期）`);
      continue;
    }
    console.log(`\n${label}  [${term.credits} 学分 / ${term.courses.length} 门]`);
    for (const c of term.courses) {
      console.log(`  - ${c.id} ${c.name}（${c.credits} 学分）`);
    }
  }
  console.log('\n===== 汇总 =====');
  console.log(`毕业所需学期数: ${plan.terms.length}`);
  console.log(`总课程数: ${plan.totalCourses} 门（上限 22 门）`);
  console.log(`总学分: ${plan.totalCredits}，额外学分: ${plan.extraCredits}`);

  if (plan.substitutions.length > 0) {
    console.log('\n===== 满足的替代路径 =====');
    for (const s of plan.substitutions) {
      console.log(
        `  ${s.forCourse} 的先修要求#${s.requirementIndex + 1}：通过课程组 [${s.chosen.join(' + ')}] 满足`,
      );
    }
  } else {
    console.log('\n无课程组替代路径被使用。');
  }
}

const result = planCourses({
  courses: catalog,
  required: requiredCourses,
  terms: makeTerms(2026, 4),
  maxTotalCourses: 22,
});

printCycles(result.cycles);
if (result.ok) {
  printPlan(result.plan);
} else {
  console.log(`\n无法生成方案：${result.reason}`);
  process.exitCode = 1;
}
