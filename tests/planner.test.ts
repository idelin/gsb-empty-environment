import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { catalog, makeTerms, requiredCourses } from '../src/catalog.ts';
import { findUnresolvableCycles } from '../src/cycles.ts';
import { planCourses } from '../src/planner.ts';
import type { Course, TermSpec } from '../src/types.ts';

const terms4y = makeTerms(2026, 4);

describe('findUnresolvableCycles', () => {
  it('识别严格先修构成的环', () => {
    const cycles = findUnresolvableCycles(catalog);
    assert.equal(cycles.length, 1);
    assert.deepEqual(cycles[0].courses, ['XX401', 'XX402']);
  });

  it('纯同修环不算无法解决的环', () => {
    const courses: Course[] = [
      { id: 'A', name: 'A', credits: 1, offered: ['fall'], coreqs: ['B'] },
      { id: 'B', name: 'B', credits: 1, offered: ['fall'], coreqs: ['A'] },
    ];
    assert.deepEqual(findUnresolvableCycles(courses), []);
  });

  it('替代路径可跳出集合时不算环', () => {
    const courses: Course[] = [
      { id: 'A', name: 'A', credits: 1, offered: ['fall'], prereqs: [{ alternatives: [['B'], ['C']] }] },
      { id: 'B', name: 'B', credits: 1, offered: ['fall'], prereqs: [{ alternatives: [['A']] }] },
      { id: 'C', name: 'C', credits: 1, offered: ['fall'] },
    ];
    assert.deepEqual(findUnresolvableCycles(courses), []);
  });
});

describe('planCourses', () => {
  const result = planCourses({
    courses: catalog,
    required: requiredCourses,
    terms: terms4y,
    maxTotalCourses: 22,
  });

  it('找到可行方案', () => {
    assert.ok(result.ok, result.ok ? '' : result.reason);
  });

  it('最早 7 个学期毕业（CS310/CS410 仅秋季开课拉长先修链）', () => {
    assert.ok(result.ok);
    assert.equal(result.plan.terms.length, 7);
  });

  it('优先单课 CS210 而非 CS215+CS216 组合（更少额外学分）', () => {
    assert.ok(result.ok);
    const sub = result.plan.substitutions.find((s) => s.forCourse === 'CS310');
    assert.ok(sub);
    assert.deepEqual(sub.chosen, ['CS210']);
    assert.equal(result.plan.extraCredits, 4);
  });

  it('同修课程 CS410/CS415 同学期修读', () => {
    assert.ok(result.ok);
    const termOf = new Map<string, number>();
    result.plan.terms.forEach((t, i) => t.courses.forEach((c) => termOf.set(c.id, i)));
    assert.equal(termOf.get('CS410'), termOf.get('CS415'));
  });

  it('遵守每学期学分与门数上限', () => {
    assert.ok(result.ok);
    for (const t of result.plan.terms) {
      assert.ok(t.credits <= t.spec.maxCredits, `学分超限: ${t.credits}`);
      assert.ok(t.courses.length <= t.spec.maxCourses, `门数超限`);
    }
  });

  it('先修均在更早学期完成', () => {
    assert.ok(result.ok);
    const termOf = new Map<string, number>();
    result.plan.terms.forEach((t, i) => t.courses.forEach((c) => termOf.set(c.id, i)));
    for (const c of catalog) {
      const myTerm = termOf.get(c.id);
      if (myTerm === undefined) continue;
      for (const req of c.prereqs ?? []) {
        const ok = req.alternatives.some((alt) =>
          alt.every((p) => (termOf.get(p) ?? Infinity) < myTerm),
        );
        assert.ok(ok, `${c.id} 的先修未满足`);
      }
    }
  });

  it('毕业要求课程陷入先修环时返回不可行', () => {
    const r = planCourses({
      courses: catalog,
      required: ['XX401'],
      terms: terms4y,
    });
    assert.ok(!r.ok);
    assert.match(r.reason, /先修环/);
    assert.equal(r.cycles.length, 1);
  });

  it('课程组替代：CS210 不开课时使用 CS215+CS216', () => {
    const noCS210 = catalog.map((c) =>
      c.id === 'CS210' ? { ...c, offered: [] } : c,
    );
    const r = planCourses({
      courses: noCS210,
      required: requiredCourses,
      terms: terms4y,
      maxTotalCourses: 22,
    });
    assert.ok(r.ok);
    const sub = r.plan.substitutions.find((s) => s.forCourse === 'CS310');
    assert.ok(sub);
    assert.deepEqual(sub.chosen, ['CS215', 'CS216']);
    assert.ok(r.plan.extraCredits > 0);
  });

  it('课程总数上限过小时不可行', () => {
    const r = planCourses({
      courses: catalog,
      required: requiredCourses,
      terms: terms4y,
      maxTotalCourses: 5,
    });
    assert.ok(!r.ok);
  });

  it('学分负载过紧时毕业学期后移', () => {
    const tight: TermSpec[] = terms4y.map((t) => ({ ...t, maxCredits: 8 }));
    const r = planCourses({
      courses: catalog,
      required: requiredCourses,
      terms: tight,
      maxTotalCourses: 22,
    });
    assert.ok(r.ok);
    assert.ok(r.plan.terms.length > 5);
  });

  it('从不开课的要求课程直接报不可行', () => {
    const r = planCourses({
      courses: catalog,
      required: ['CS101', 'CS201'],
      terms: [{ year: 2026, season: 'spring', maxCredits: 18, maxCourses: 5 }],
    });
    assert.ok(!r.ok);
    assert.match(r.reason, /从不开课/);
  });
});
