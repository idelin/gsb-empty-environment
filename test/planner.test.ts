import { test } from 'node:test';
import assert from 'node:assert/strict';
import { plan } from '../src/planner.ts';
import { findUnresolvableCycles } from '../src/cycles.ts';
import { sampleInput } from '../src/sample.ts';
import type { Course, PlannerInput, PlanResult } from '../src/types.ts';

const semOf = (r: PlanResult, id: string): number =>
  r.semesters.find((s) => s.courses.includes(id))!.index;

function makeInput(overrides: Partial<PlannerInput>): PlannerInput {
  return {
    courses: [],
    required: [],
    requiredCredits: 0,
    maxCreditsPerSemester: 18,
    startTerm: 'fall',
    maxSemesters: 10,
    ...overrides,
  };
}

const c = (
  id: string,
  credits: number,
  offered: ('fall' | 'spring')[] = ['fall', 'spring'],
  prereqs: (string | { anyOf: string[] })[] = [],
  coreqs: string[] = [],
): Course => ({ id, name: id, credits, offered, prereqs, coreqs });

test('示例体系：8 学期毕业、0 额外学分、约束全部满足', () => {
  const r = plan(sampleInput);
  assert.equal(r.feasible, true);
  assert.equal(r.semesters.length, 8);
  assert.equal(r.extraCredits, 0);
  assert.ok(r.totalCourses <= 22);
  const map = new Map(sampleInput.courses.map((x) => [x.id, x]));
  for (const s of r.semesters) {
    assert.ok(s.credits <= sampleInput.maxCreditsPerSemester);
    for (const id of s.courses) {
      assert.ok(map.get(id)!.offered.includes(s.term), `${id} 开课学期不符`);
    }
  }
  for (const course of sampleInput.courses) {
    if (!r.semesters.some((s) => s.courses.includes(course.id))) continue;
    for (const p of course.prereqs) {
      const opts = typeof p === 'string' ? [p] : p.anyOf;
      const ok = opts.some(
        (o) =>
          r.semesters.some((s) => s.courses.includes(o)) &&
          semOf(r, o) < semOf(r, course.id),
      );
      assert.ok(ok, `${course.id} 先修 ${opts.join('/')} 未满足`);
    }
    for (const co of course.coreqs) {
      assert.ok(semOf(r, co) <= semOf(r, course.id), `${course.id} 同修 ${co} 未满足`);
    }
  }
  // 替代路径：CS320/CS410 各选了一个组内选项
  assert.equal(r.substitutions.length, 2);
  assert.ok(r.substitutions.every((s) => s.options.includes(s.chosen)));
});

test('纯先修环：无法通过同修解决，判定无解', () => {
  const courses = [c('A', 3, ['fall', 'spring'], ['B']), c('B', 3, ['fall', 'spring'], ['A'])];
  const cycles = findUnresolvableCycles(courses);
  assert.equal(cycles.length, 1);
  assert.equal(cycles[0].involvesSubstitution, false);
  const r = plan(makeInput({ courses, required: ['A', 'B'], requiredCredits: 6 }));
  assert.equal(r.feasible, false);
});

test('纯同修环：同学期修读即可解决', () => {
  const courses = [
    c('A', 3, ['fall'], [], ['B']),
    c('B', 3, ['fall'], [], ['A']),
  ];
  const cycles = findUnresolvableCycles(courses);
  assert.equal(cycles.length, 0);
  const r = plan(makeInput({ courses, required: ['A', 'B'], requiredCredits: 6 }));
  assert.equal(r.feasible, true);
  assert.equal(semOf(r, 'A'), semOf(r, 'B'));
});

test('先修+同修混合环：含严格边，无法解决', () => {
  const courses = [
    c('A', 3, ['fall', 'spring'], ['B']),
    c('B', 3, ['fall', 'spring'], [], ['A']),
  ];
  const cycles = findUnresolvableCycles(courses);
  assert.equal(cycles.length, 1);
  assert.ok(cycles[0].courses.includes('A') && cycles[0].courses.includes('B'));
});

test('课程组替代：换选项避开先修环', () => {
  // C 需要 A 或 B；A 又需要 C（成环）；B 无依赖 → 选 B 可解
  const courses = [
    c('C', 3, ['fall', 'spring'], [{ anyOf: ['A', 'B'] }]),
    c('A', 3, ['fall', 'spring'], ['C']),
    c('B', 3),
  ];
  const cycles = findUnresolvableCycles(courses);
  assert.equal(cycles.length, 1);
  assert.equal(cycles[0].involvesSubstitution, true);
  const r = plan(makeInput({ courses, required: ['C'], requiredCredits: 3 }));
  assert.equal(r.feasible, true);
  assert.equal(r.substitutions.length, 1);
  assert.equal(r.substitutions[0].chosen, 'B');
});

test('学分负载限制会拉长毕业时间', () => {
  const courses = [c('A', 5), c('B', 5), c('C', 5), c('D', 5)];
  const r = plan(
    makeInput({ courses, required: ['A', 'B', 'C', 'D'], requiredCredits: 20, maxCreditsPerSemester: 10 }),
  );
  assert.equal(r.feasible, true);
  assert.equal(r.semesters.length, 2);
  const r2 = plan(
    makeInput({ courses, required: ['A', 'B', 'C', 'D'], requiredCredits: 20, maxCreditsPerSemester: 5 }),
  );
  assert.equal(r2.semesters.length, 4);
});

test('先修链决定最早毕业学期数', () => {
  const courses = [
    c('A', 3),
    c('B', 3, ['fall', 'spring'], ['A']),
    c('C', 3, ['fall', 'spring'], ['B']),
  ];
  const r = plan(makeInput({ courses, required: ['C'], requiredCredits: 9 }));
  assert.equal(r.feasible, true);
  assert.equal(r.semesters.length, 3);
});

test('开课学期限制：仅秋季开课无法排在春季', () => {
  const courses = [c('A', 3, ['fall']), c('B', 3, ['fall', 'spring'], ['A'])];
  const r = plan(makeInput({ courses, required: ['B'], requiredCredits: 6 }));
  assert.equal(r.feasible, true);
  assert.equal(r.semesters[semOf(r, 'A') - 1].term, 'fall');
});

test('超过 22 门课上限判定无解', () => {
  const courses: Course[] = [];
  const required: string[] = [];
  for (let i = 0; i < 23; i++) {
    courses.push(c(`X${i}`, 1));
    required.push(`X${i}`);
  }
  const r = plan(makeInput({ courses, required, requiredCredits: 23, maxCreditsPerSemester: 30 }));
  assert.equal(r.feasible, false);
});

test('选修凑学分：额外学分最小化', () => {
  const courses = [c('A', 4), c('E1', 2), c('E2', 3)];
  const r = plan(
    makeInput({ courses, required: ['A'], requiredCredits: 6, electives: ['E1', 'E2'] }),
  );
  assert.equal(r.feasible, true);
  assert.equal(r.totalCredits, 6); // 选 E1(2) 而非 E2(3)
  assert.equal(r.extraCredits, 0);
});
