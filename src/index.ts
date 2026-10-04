import { plan } from './planner.ts';
import { sampleInput, sampleCourses } from './sample.ts';
import type { PlanResult } from './types.ts';

function format(result: PlanResult, input: typeof sampleInput): string {
  const nameOf = new Map(input.courses.map((c) => [c.id, c.name]));
  const lines: string[] = [];

  if (result.cycles.length > 0) {
    lines.push('⚠ 检测到无法通过同修解决的先修环：');
    for (const c of result.cycles) {
      const tag = c.involvesSubstitution ? '（涉及课程组替代，更换选项或可消除）' : '';
      lines.push(`  - ${c.courses.join(' → ')} ${tag}`);
      lines.push(`    ${c.reason}`);
    }
    lines.push('');
  }

  if (!result.feasible) {
    lines.push(`无解：${result.message ?? '未知原因'}`);
    return lines.join('\n');
  }

  lines.push(`最早毕业：${result.semesters.length} 个学期`);
  lines.push(
    `总学分 ${result.totalCredits}（毕业要求 ${input.requiredCredits}，额外学分 ${result.extraCredits}），共 ${result.totalCourses} 门课`,
  );
  lines.push('');

  for (const s of result.semesters) {
    const term = s.term === 'fall' ? '秋' : '春';
    lines.push(`第 ${s.index} 学期（${term}）— ${s.credits} 学分`);
    for (const id of s.courses) {
      const c = sampleCourses.find((x) => x.id === id)!;
      lines.push(`  ${id}  ${nameOf.get(id)}  ${c.credits} 学分`);
    }
  }

  if (result.substitutions.length > 0) {
    lines.push('');
    lines.push('满足的课程组替代路径：');
    for (const sub of result.substitutions) {
      lines.push(
        `  ${sub.forCourse} 的先修 [${sub.options.join(' / ')}] → 选择 ${sub.chosen}（${nameOf.get(sub.chosen)}）`,
      );
    }
  }
  return lines.join('\n');
}

const result = plan(sampleInput);
console.log(format(result, sampleInput));
