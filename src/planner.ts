import type {
  Course,
  PlannerInput,
  PlanResult,
  SemesterPlan,
  SubstitutionChoice,
  Term,
} from './types.ts';
import { findUnresolvableCycles } from './cycles.ts';

const DEFAULT_MAX_COURSES = 22;

interface Expansion {
  mustTake: Set<string>;
  substitutions: SubstitutionChoice[];
}

/**
 * 展开毕业需求：必修课 + 单课先修 + 同修 全部递归加入；
 * 课程组先修（anyOf）通过分支各选一个选项，产生多个候选展开。
 */
function expandRequirements(
  required: string[],
  courseMap: Map<string, Course>,
  maxCourses: number,
): Expansion[] {
  const results: Expansion[] = [];
  const seen = new Set<string>();

  const recurse = (mustTake: Set<string>, subs: SubstitutionChoice[]): void => {
    if (mustTake.size > maxCourses) return;
    // 找一个尚未处理的课程组先修
    for (const id of mustTake) {
      const course = courseMap.get(id)!;
      for (const p of course.prereqs) {
        if (typeof p === 'string') {
          if (!mustTake.has(p)) {
            const next = new Set(mustTake);
            next.add(p);
            // 单课先修/同修 闭包在下方统一处理
            recurse(closeSingles(next, courseMap), subs);
            return;
          }
        } else {
          const satisfied = p.anyOf.find((o) => mustTake.has(o));
          if (satisfied) {
            // 已被集合中某门课满足，记录替代路径（若未记录过）
            if (!subs.some((s) => s.forCourse === id && s.options.join() === p.anyOf.join())) {
              recurse(mustTake, [...subs, { forCourse: id, options: p.anyOf, chosen: satisfied }]);
              return;
            }
          } else {
            // 分支：组内每个选项都是一种替代路径
            for (const opt of p.anyOf) {
              if (!courseMap.has(opt)) continue;
              const next = new Set(mustTake);
              next.add(opt);
              recurse(closeSingles(next, courseMap), [
                ...subs,
                { forCourse: id, options: p.anyOf, chosen: opt },
              ]);
            }
            return;
          }
        }
      }
    }
    const key = [...mustTake].sort().join(',');
    if (!seen.has(key)) {
      seen.add(key);
      results.push({ mustTake, substitutions: subs });
    }
  };

  const closeSingles = (set: Set<string>, map: Map<string, Course>): Set<string> => {
    const out = new Set(set);
    const queue = [...out];
    while (queue.length > 0) {
      const id = queue.pop()!;
      const c = map.get(id)!;
      const deps: string[] = [
        ...c.prereqs.filter((p): p is string => typeof p === 'string'),
        ...c.coreqs,
      ];
      for (const d of deps) {
        if (!out.has(d) && map.has(d)) {
          out.add(d);
          queue.push(d);
        }
      }
    }
    return out;
  };

  recurse(closeSingles(new Set(required), courseMap), []);
  return results;
}

/**
 * 严格先修链深度（含已选定的替代选项）。
 * 返回 Infinity 表示所选依赖中存在先修环，该展开不可行。
 */
function strictDepths(
  mustTake: Set<string>,
  subs: SubstitutionChoice[],
  courseMap: Map<string, Course>,
): Map<string, number> {
  const chosen = new Map<string, string>();
  for (const s of subs) chosen.set(`${s.forCourse}|${s.options.join(',')}`, s.chosen);
  const depth = new Map<string, number>();
  const visiting = new Set<string>();

  const dfs = (id: string): number => {
    if (depth.has(id)) return depth.get(id)!;
    if (visiting.has(id)) return Infinity;
    visiting.add(id);
    const c = courseMap.get(id)!;
    let d = 0;
    for (const p of c.prereqs) {
      let dep: string | undefined;
      if (typeof p === 'string') dep = p;
      else dep = chosen.get(`${id}|${p.anyOf.join(',')}`) ?? p.anyOf.find((o) => mustTake.has(o));
      if (dep !== undefined && mustTake.has(dep)) {
        const dd = dfs(dep);
        if (dd === Infinity) {
          depth.set(id, Infinity);
          visiting.delete(id);
          return Infinity;
        }
        d = Math.max(d, dd + 1);
      }
    }
    visiting.delete(id);
    depth.set(id, d);
    return d;
  };

  for (const id of mustTake) dfs(id);
  return depth;
}

interface Assignment {
  sem: Map<string, number>;
  creditsPerSem: number[];
}

/** 回溯搜索：把 mustTake 中的课程分配到 1..T 学期 */
function scheduleSemesters(
  mustTake: Set<string>,
  subs: SubstitutionChoice[],
  courseMap: Map<string, Course>,
  T: number,
  termOf: (sem: number) => Term,
  maxCredits: number,
): Assignment | null {
  const depth = strictDepths(mustTake, subs, courseMap);
  for (const id of mustTake) if (depth.get(id) === Infinity) return null;

  // 先修深度小的先排（先修课一定先于依赖它的课被分配）
  const order = [...mustTake].sort((a, b) => depth.get(a)! - depth.get(b)!);
  const chosen = new Map<string, string>();
  for (const s of subs) chosen.set(`${s.forCourse}|${s.options.join(',')}`, s.chosen);

  const strictDepsOf = (id: string): string[] => {
    const c = courseMap.get(id)!;
    const deps: string[] = [];
    for (const p of c.prereqs) {
      if (typeof p === 'string') {
        if (mustTake.has(p)) deps.push(p);
      } else {
        const opt = chosen.get(`${id}|${p.anyOf.join(',')}`) ?? p.anyOf.find((o) => mustTake.has(o));
        if (opt && mustTake.has(opt)) deps.push(opt);
      }
    }
    return deps;
  };
  const coreqsOf = (id: string): string[] =>
    courseMap.get(id)!.coreqs.filter((co) => mustTake.has(co));

  const sem = new Map<string, number>();
  const creditsPerSem = new Array<number>(T + 1).fill(0);

  const consistent = (id: string, s: number): boolean => {
    const c = courseMap.get(id)!;
    if (!c.offered.includes(termOf(s))) return false;
    if (creditsPerSem[s] + c.credits > maxCredits) return false;
    for (const dep of strictDepsOf(id)) {
      const ds = sem.get(dep);
      if (ds !== undefined && ds >= s) return false; // 先修必须严格更早
    }
    for (const co of coreqsOf(id)) {
      const cs = sem.get(co);
      if (cs !== undefined && cs > s) return false; // 同修须同学期或更早
    }
    // 反向：已分配课程中依赖 id 的约束
    for (const [other, os] of sem) {
      if (strictDepsOf(other).includes(id) && s >= os) return false;
      if (coreqsOf(other).includes(id) && s > os) return false;
    }
    return true;
  };

  const backtrack = (i: number): boolean => {
    if (i === order.length) return true;
    const id = order[i];
    const c = courseMap.get(id)!;
    // 下界：所有已分配严格先修的学期 + 1
    let lo = 1;
    for (const dep of strictDepsOf(id)) {
      const ds = sem.get(dep);
      if (ds !== undefined) lo = Math.max(lo, ds + 1);
    }
    for (let s = lo; s <= T; s++) {
      if (!consistent(id, s)) continue;
      sem.set(id, s);
      creditsPerSem[s] += c.credits;
      if (backtrack(i + 1)) return true;
      sem.delete(id);
      creditsPerSem[s] -= c.credits;
    }
    return false;
  };

  return backtrack(0) ? { sem, creditsPerSem } : null;
}

/** 选修凑学分：按总学分升序枚举子集，尝试装入剩余学期容量 */
function fillElectives(
  assignment: Assignment,
  mustTake: Set<string>,
  pool: Course[],
  needed: number,
  slotsLeft: number,
  T: number,
  termOf: (sem: number) => Term,
  maxCredits: number,
): { chosen: Course[]; placement: Map<string, number> } | null {
  if (needed <= 0) return { chosen: [], placement: new Map() };
  // 生成所有不超门数上限的子集，按 (总学分, 门数) 升序
  const subsets: { set: Course[]; credits: number }[] = [];
  const gen = (i: number, acc: Course[], credits: number): void => {
    if (acc.length > slotsLeft) return;
    if (credits >= needed) {
      subsets.push({ set: [...acc], credits });
      return; // 已达标，继续加课只会更多学分（学分均为正）
    }
    if (i >= pool.length) return;
    gen(i + 1, acc, credits);
    acc.push(pool[i]);
    gen(i + 1, acc, credits + pool[i].credits);
    acc.pop();
  };
  gen(0, [], 0);
  subsets.sort((a, b) => a.credits - b.credits || a.set.length - b.set.length);

  for (const { set } of subsets) {
    const placement = new Map<string, number>();
    const caps = assignment.creditsPerSem.slice();
    const items = [...set].sort((a, b) => b.credits - a.credits);
    const place = (k: number): boolean => {
      if (k === items.length) return true;
      const c = items[k];
      for (let s = 1; s <= T; s++) {
        if (!c.offered.includes(termOf(s))) continue;
        if (caps[s] + c.credits > maxCredits) continue;
        caps[s] += c.credits;
        placement.set(c.id, s);
        if (place(k + 1)) return true;
        caps[s] -= c.credits;
        placement.delete(c.id);
      }
      return false;
    };
    if (place(0)) return { chosen: set, placement };
  }
  return null;
}

export function plan(input: PlannerInput): PlanResult {
  const maxCourses = input.maxCourses ?? DEFAULT_MAX_COURSES;
  const courseMap = new Map(input.courses.map((c) => [c.id, c]));
  const cycles = findUnresolvableCycles(input.courses);
  const base: Omit<PlanResult, 'feasible' | 'message'> = {
    cycles,
    semesters: [],
    substitutions: [],
    totalCredits: 0,
    extraCredits: 0,
    totalCourses: 0,
  };

  const unknown = input.required.filter((id) => !courseMap.has(id));
  if (unknown.length > 0) {
    return { ...base, feasible: false, message: `未知课程: ${unknown.join(', ')}` };
  }

  const termOf = (sem: number): Term => {
    const other: Term = input.startTerm === 'fall' ? 'spring' : 'fall';
    return sem % 2 === 1 ? input.startTerm : other;
  };

  const expansions = expandRequirements(input.required, courseMap, maxCourses);
  if (expansions.length === 0) {
    return { ...base, feasible: false, message: `必修闭包超过 ${maxCourses} 门上限，无解` };
  }

  const electivePool = (
    input.electives ??
    input.courses
      .filter(
        (c) =>
          !input.required.includes(c.id) && c.prereqs.length === 0 && c.coreqs.length === 0,
      )
      .map((c) => c.id)
  )
    .map((id) => courseMap.get(id))
    .filter((c): c is Course => c !== undefined);

  // 按必修课学分升序，优先尝试总成本更低的替代路径
  const expansionsWithDepth = expansions
    .map((e) => ({
      ...e,
      depth: strictDepths(e.mustTake, e.substitutions, courseMap),
      credits: [...e.mustTake].reduce((s, id) => s + courseMap.get(id)!.credits, 0),
    }))
    .filter((e) => [...e.mustTake].every((id) => e.depth.get(id) !== Infinity))
    .sort((a, b) => a.credits - b.credits);

  if (expansionsWithDepth.length === 0) {
    return { ...base, feasible: false, message: '所有替代路径均含先修环，无法毕业' };
  }

  for (let T = 1; T <= input.maxSemesters; T++) {
    let best: PlanResult | null = null;
    for (const e of expansionsWithDepth) {
      const minSemByDepth = Math.max(...[...e.mustTake].map((id) => e.depth.get(id)!)) + 1;
      const minSemByCredits = Math.ceil(e.credits / input.maxCreditsPerSemester);
      if (T < Math.max(minSemByDepth, minSemByCredits)) continue;

      const assignment = scheduleSemesters(
        e.mustTake,
        e.substitutions,
        courseMap,
        T,
        termOf,
        input.maxCreditsPerSemester,
      );
      if (!assignment) continue;

      const needed = input.requiredCredits - e.credits;
      const pool = electivePool.filter((c) => !e.mustTake.has(c.id));
      const fill = fillElectives(
        assignment,
        e.mustTake,
        pool,
        needed,
        maxCourses - e.mustTake.size,
        T,
        termOf,
        input.maxCreditsPerSemester,
      );
      if (!fill) continue;

      const semOf = new Map(assignment.sem);
      for (const [id, s] of fill.placement) semOf.set(id, s);
      const semesters: SemesterPlan[] = [];
      for (let s = 1; s <= T; s++) {
        const ids = [...semOf.entries()].filter(([, v]) => v === s).map(([k]) => k);
        semesters.push({
          index: s,
          term: termOf(s),
          courses: ids,
          credits: ids.reduce((sum, id) => sum + courseMap.get(id)!.credits, 0),
        });
      }
      const totalCredits = e.credits + fill.chosen.reduce((s, c) => s + c.credits, 0);
      const candidate: PlanResult = {
        ...base,
        feasible: true,
        semesters,
        substitutions: e.substitutions,
        totalCredits,
        extraCredits: Math.max(0, totalCredits - input.requiredCredits),
        totalCourses: e.mustTake.size + fill.chosen.length,
      };
      if (!best || candidate.totalCredits < best.totalCredits) best = candidate;
    }
    if (best) return best; // 最早毕业学期数下，取总学分最少者
  }

  return {
    ...base,
    feasible: false,
    message: `在 ${input.maxSemesters} 个学期、每学期 ${input.maxCreditsPerSemester} 学分、共 ${maxCourses} 门课的限制下无解`,
  };
}
