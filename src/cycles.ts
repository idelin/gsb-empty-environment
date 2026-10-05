import type { Course, CycleEdge, CycleReport } from './types.ts';

/**
 * 识别“无法通过同修解决”的先修环。
 *
 * 思路：同修依赖允许同学期修读，因此纯同修环可解；
 * 先修依赖必须严格更早，若某门课无论如何选择替代路径，
 * 其先修都落回某个集合内部，则该集合中的课程互相锁死。
 *
 * 算法：求最大“被困集合” U（不动点迭代）——
 * 反复移除“每条先修要求都存在一条完全落在 U 之外的替代路径”的课程，
 * 剩余课程即被困在环中；再在 U 内构图取强连通分量作为 witness。
 */
export function findUnresolvableCycles(courses: Course[]): CycleReport[] {
  const byId = new Map(courses.map((c) => [c.id, c]));
  const trapped = new Set(byId.keys());

  let changed = true;
  while (changed) {
    changed = false;
    for (const id of [...trapped]) {
      const course = byId.get(id)!;
      const reqs = course.prereqs ?? [];
      const canEscape = reqs.every((req) =>
        req.alternatives.some((alt) => alt.every((p) => !trapped.has(p))),
      );
      if (canEscape) {
        trapped.delete(id);
        changed = true;
      }
    }
  }

  if (trapped.size === 0) return [];

  // 在被困集合内构建 witness 图：v -> u 表示 v 的某条先修要求中，
  // 存在一条完全落在被困集合内且包含 u 的替代路径。
  const edges: CycleEdge[] = [];
  const adj = new Map<string, string[]>();
  for (const id of trapped) {
    const course = byId.get(id)!;
    const targets = new Set<string>();
    (course.prereqs ?? []).forEach((req, requirementIndex) => {
      for (const alt of req.alternatives) {
        if (alt.length > 0 && alt.every((p) => trapped.has(p))) {
          for (const u of alt) {
            if (u !== id) {
              edges.push({ from: id, to: u, requirementIndex });
              targets.add(u);
            }
          }
        }
      }
    });
    adj.set(id, [...targets]);
  }

  const reports: CycleReport[] = [];
  for (const scc of stronglyConnectedComponents([...trapped], adj)) {
    const isSelfLoop =
      scc.length === 1 &&
      (byId.get(scc[0])!.prereqs ?? []).some((req) =>
        req.alternatives.some((alt) => alt.includes(scc[0])),
      );
    if (scc.length > 1 || isSelfLoop) {
      const inScc = new Set(scc);
      reports.push({
        courses: scc.sort(),
        edges: edges.filter((e) => inScc.has(e.from) && inScc.has(e.to)),
      });
    }
  }
  return reports;
}

/** 被困集合（含未形成环但被环锁死的课程），供规划器判断可行性 */
export function trappedCourses(courses: Course[]): Set<string> {
  const byId = new Map(courses.map((c) => [c.id, c]));
  const trapped = new Set(byId.keys());
  let changed = true;
  while (changed) {
    changed = false;
    for (const id of [...trapped]) {
      const course = byId.get(id)!;
      const canEscape = (course.prereqs ?? []).every((req) =>
        req.alternatives.some((alt) => alt.every((p) => !trapped.has(p))),
      );
      if (canEscape) {
        trapped.delete(id);
        changed = true;
      }
    }
  }
  return trapped;
}

function stronglyConnectedComponents(
  nodes: string[],
  adj: Map<string, string[]>,
): string[][] {
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const result: string[][] = [];
  let counter = 0;

  const visit = (v: string): void => {
    index.set(v, counter);
    low.set(v, counter);
    counter += 1;
    stack.push(v);
    onStack.add(v);
    for (const w of adj.get(v) ?? []) {
      if (!index.has(w)) {
        visit(w);
        low.set(v, Math.min(low.get(v)!, low.get(w)!));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v)!, index.get(w)!));
      }
    }
    if (low.get(v) === index.get(v)) {
      const scc: string[] = [];
      let w: string;
      do {
        w = stack.pop()!;
        onStack.delete(w);
        scc.push(w);
      } while (w !== v);
      result.push(scc);
    }
  };

  for (const v of nodes) {
    if (!index.has(v)) visit(v);
  }
  return result;
}
