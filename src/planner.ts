import { findUnresolvableCycles, trappedCourses } from './cycles.ts';
import type {
  Course,
  Plan,
  PlanResult,
  PlannerConfig,
  SubstitutionChoice,
  TermSchedule,
} from './types.ts';

const DEFAULT_MAX_TOTAL_COURSES = 22;
/** 单学期可枚举子集的课程数上限，防止组合爆炸 */
const MAX_ENUMERABLE = 16;

interface State {
  completed: Set<string>;
  credits: number;
  courseCount: number;
  /** schedule[i] = 第 i 学期修读的课程 id 列表 */
  schedule: string[][];
}

export function planCourses(config: PlannerConfig): PlanResult {
  const { courses, required, terms } = config;
  const maxTotalCourses = config.maxTotalCourses ?? DEFAULT_MAX_TOTAL_COURSES;

  const byId = new Map<string, Course>();
  for (const c of courses) {
    if (byId.has(c.id)) {
      return fail(`课程 id 重复: ${c.id}`, courses);
    }
    byId.set(c.id, c);
  }
  for (const id of required) {
    if (!byId.has(id)) {
      return fail(`毕业要求中包含不存在的课程: ${id}`, courses);
    }
  }
  for (const c of courses) {
    for (const req of c.prereqs ?? []) {
      for (const alt of req.alternatives) {
        for (const p of alt) {
          if (!byId.has(p)) return fail(`课程 ${c.id} 的先修引用了不存在的课程: ${p}`, courses);
        }
      }
    }
    for (const co of c.coreqs ?? []) {
      if (!byId.has(co)) return fail(`课程 ${c.id} 的同修引用了不存在的课程: ${co}`, courses);
    }
  }

  const cycles = findUnresolvableCycles(courses);
  const trapped = trappedCourses(courses);
  const blockedRequired = required.filter((id) => trapped.has(id));
  if (blockedRequired.length > 0) {
    return {
      ok: false,
      reason: `毕业要求课程陷入无法通过同修解决的先修环: ${blockedRequired.join(', ')}`,
      cycles,
    };
  }

  const requiredSet = new Set(required);
  const offeredSeasons = new Set(terms.map((t) => t.season));
  const neverOffered = required.filter(
    (id) => !(byId.get(id)!.offered ?? []).some((s) => offeredSeasons.has(s)),
  );
  if (neverOffered.length > 0) {
    return {
      ok: false,
      reason: `以下毕业要求课程在候选学期中从不开课: ${neverOffered.join(', ')}`,
      cycles,
    };
  }

  const coversRequired = (completed: Set<string>): boolean => {
    for (const id of requiredSet) if (!completed.has(id)) return false;
    return true;
  };

  let states: State[] = [
    { completed: new Set(), credits: 0, courseCount: 0, schedule: [] },
  ];

  for (let t = 0; t < terms.length; t += 1) {
    const term = terms[t];
    const next = new Map<string, State>();

    for (const state of states) {
      const eligible = courses.filter(
        (c) =>
          !state.completed.has(c.id) &&
          c.offered.includes(term.season) &&
          prereqsSatisfied(c, state.completed),
      );
      if (eligible.length > MAX_ENUMERABLE) {
        return fail(
          `第 ${t + 1} 学期可选课程过多（${eligible.length} 门），无法枚举组合`,
          courses,
        );
      }
      for (const subset of enumerateSubsets(eligible, term, state, maxTotalCourses)) {
        if (!coreqsSatisfied(subset, state.completed, byId)) continue;
        const completed = new Set(state.completed);
        let credits = state.credits;
        for (const c of subset) {
          completed.add(c.id);
          credits += c.credits;
        }
        const candidate: State = {
          completed,
          credits,
          courseCount: state.courseCount + subset.length,
          schedule: [...state.schedule, subset.map((c) => c.id)],
        };
        const key = [...completed].sort().join('');
        const prev = next.get(key);
        if (
          !prev ||
          candidate.credits < prev.credits ||
          (candidate.credits === prev.credits && candidate.courseCount < prev.courseCount)
        ) {
          next.set(key, candidate);
        }
      }
    }

    states = pruneDominated([...next.values()]);

    const graduates = states.filter((s) => coversRequired(s.completed));
    if (graduates.length > 0) {
      graduates.sort((a, b) => a.credits - b.credits || a.courseCount - b.courseCount);
      const best = graduates[0];
      return { ok: true, plan: buildPlan(best, t, config, byId), cycles };
    }
  }

  return {
    ok: false,
    reason: `在 ${terms.length} 个候选学期内无法完成全部毕业要求（课程总数上限 ${maxTotalCourses} 门）`,
    cycles,
  };
}

function fail(reason: string, courses: Course[]): PlanResult {
  return { ok: false, reason, cycles: findUnresolvableCycles(courses) };
}

/** 先修：每条要求至少一条替代路径已全部在更早学期完成 */
function prereqsSatisfied(course: Course, completed: Set<string>): boolean {
  return (course.prereqs ?? []).every((req) =>
    req.alternatives.some((alt) => alt.every((p) => completed.has(p))),
  );
}

/** 同修：所选课程的同修必须已在更早学期完成，或与本学期一同修读 */
function coreqsSatisfied(
  subset: Course[],
  completed: Set<string>,
  byId: Map<string, Course>,
): boolean {
  const available = new Set(completed);
  for (const c of subset) available.add(c.id);
  return subset.every((c) => (c.coreqs ?? []).every((co) => available.has(co)));
}

function* enumerateSubsets(
  eligible: Course[],
  term: { maxCredits: number; maxCourses: number },
  state: State,
  maxTotalCourses: number,
): Generator<Course[]> {
  const maxCourses = Math.min(
    term.maxCourses,
    maxTotalCourses - state.courseCount,
    eligible.length,
  );
  const picked: Course[] = [];
  let pickedCredits = 0;

  function* dfs(start: number): Generator<Course[]> {
    yield [...picked];
    if (picked.length >= maxCourses) return;
    for (let i = start; i < eligible.length; i += 1) {
      const c = eligible[i];
      if (pickedCredits + c.credits > term.maxCredits) continue;
      picked.push(c);
      pickedCredits += c.credits;
      yield* dfs(i + 1);
      picked.pop();
      pickedCredits -= c.credits;
    }
  }
  yield* dfs(0);
}

/** 支配剪枝：若状态 B 已完成课程 ⊇ A 且学分 ≤ A，则 A 无保留价值 */
function pruneDominated(states: State[]): State[] {
  return states.filter(
    (a) =>
      !states.some(
        (b) =>
          b !== a &&
          b.credits <= a.credits &&
          b.courseCount <= a.courseCount &&
          isSuperset(b.completed, a.completed) &&
          (b.credits < a.credits ||
            b.courseCount < a.courseCount ||
            b.completed.size > a.completed.size),
      ),
  );
}

function isSuperset(a: Set<string>, b: Set<string>): boolean {
  for (const x of b) if (!a.has(x)) return false;
  return true;
}

function buildPlan(
  best: State,
  lastTermIndex: number,
  config: PlannerConfig,
  byId: Map<string, Course>,
): Plan {
  const termSchedules: TermSchedule[] = [];
  for (let t = 0; t <= lastTermIndex; t += 1) {
    const ids = best.schedule[t] ?? [];
    const termCourses = ids.map((id) => byId.get(id)!);
    termSchedules.push({
      spec: config.terms[t],
      courses: termCourses,
      credits: termCourses.reduce((sum, c) => sum + c.credits, 0),
    });
  }

  // 每门课完成学期，用于回放替代路径
  const termOf = new Map<string, number>();
  best.schedule.forEach((ids, t) => {
    for (const id of ids) termOf.set(id, t);
  });

  const substitutions: SubstitutionChoice[] = [];
  for (const id of [...best.completed].sort()) {
    const course = byId.get(id)!;
    const myTerm = termOf.get(id)!;
    (course.prereqs ?? []).forEach((req, requirementIndex) => {
      const hasChoice =
        req.alternatives.length > 1 || req.alternatives.some((a) => a.length > 1);
      if (!hasChoice) return;
      const usable = req.alternatives.filter((alt) =>
        alt.every((p) => (termOf.get(p) ?? Infinity) < myTerm),
      );
      if (usable.length === 0) return;
      usable.sort(
        (a, b) =>
          a.length - b.length ||
          creditsOf(a, byId) - creditsOf(b, byId) ||
          a.join(',').localeCompare(b.join(',')),
      );
      substitutions.push({ forCourse: id, requirementIndex, chosen: usable[0] });
    });
  }

  const requiredCredits = config.required
    .filter((id) => best.completed.has(id))
    .reduce((sum, id) => sum + byId.get(id)!.credits, 0);

  return {
    terms: termSchedules,
    totalCredits: best.credits,
    extraCredits: best.credits - requiredCredits,
    totalCourses: best.courseCount,
    substitutions,
  };
}

function creditsOf(ids: string[], byId: Map<string, Course>): number {
  return ids.reduce((sum, id) => sum + byId.get(id)!.credits, 0);
}
