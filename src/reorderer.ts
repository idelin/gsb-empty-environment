/**
 * 事件重排器：在部分先后约束（precedence）与对象生命周期状态约束
 * （create -> update* -> delete）下，求字典序最小的合法全序。
 *
 * 设计要点：
 * - 状态约束不作为事后检查，而是与先后约束一起融入逐步构造：
 *   每个位置都通过“可行性预言机”判断剩余事件是否仍可完成，
 *   从而保证得到的全序既满足先后关系又满足状态机约束，且字典序最小。
 * - 无解时不输出笼统的“整体无解”：改用纯局部贪心走出一条
 *   字典序最小的极大局部合法前缀（阻塞前缀），并报告每个剩余事件
 *   被哪些具体约束阻塞。
 */

export type Op = "create" | "update" | "delete";

export interface EventSpec {
  id: string;
  object: string;
  op: Op;
}

export interface Problem {
  events: EventSpec[];
  /** [before, after] 表示 before 必须排在 after 之前 */
  precedence?: Array<[string, string]>;
}

export interface BlockedEvent {
  event: string;
  reasons: string[];
}

export type SolveResult =
  | { status: "ok"; order: string[] }
  | {
      status: "unsat";
      /** 字典序最小的极大局部合法前缀：再往下任何事件都被约束阻塞 */
      blockingPrefix: string[];
      /** 每个未放置事件及其被阻塞的具体约束 */
      blocked: BlockedEvent[];
      /** 若先后约束中存在环，给出其中一个环 */
      cycle: string[] | null;
    };

export const MAX_EVENTS = 22;

export function validate(problem: Problem): string[] {
  const errors: string[] = [];
  if (!problem || typeof problem !== "object" || !Array.isArray(problem.events)) {
    return ["events must be an array"];
  }
  if (problem.events.length > MAX_EVENTS) {
    errors.push(`too many events: ${problem.events.length} > ${MAX_EVENTS}`);
  }
  const ids = new Set<string>();
  const createOf = new Map<string, string>();
  const deleteOf = new Map<string, string>();
  for (const e of problem.events) {
    if (!e || typeof e.id !== "string" || e.id === "") {
      errors.push("every event needs a non-empty string id");
      continue;
    }
    if (ids.has(e.id)) errors.push(`duplicate event id: '${e.id}'`);
    ids.add(e.id);
    if (typeof e.object !== "string" || e.object === "") {
      errors.push(`event '${e.id}': object must be a non-empty string`);
    }
    if (e.op !== "create" && e.op !== "update" && e.op !== "delete") {
      errors.push(`event '${e.id}': op must be one of create/update/delete`);
    }
    if (e.op === "create") {
      const prev = createOf.get(e.object);
      if (prev !== undefined) {
        errors.push(`object '${e.object}': multiple create events ('${prev}', '${e.id}')`);
      } else createOf.set(e.object, e.id);
    }
    if (e.op === "delete") {
      const prev = deleteOf.get(e.object);
      if (prev !== undefined) {
        errors.push(`object '${e.object}': multiple delete events ('${prev}', '${e.id}')`);
      } else deleteOf.set(e.object, e.id);
    }
  }
  for (const pair of problem.precedence ?? []) {
    if (!Array.isArray(pair) || pair.length !== 2) {
      errors.push("each precedence entry must be a [before, after] pair");
      continue;
    }
    const [a, b] = pair;
    if (!ids.has(a) || !ids.has(b)) {
      errors.push(`precedence references unknown event: ['${a}', '${b}']`);
    } else if (a === b) {
      errors.push(`self-referential precedence: ['${a}', '${a}']`);
    }
  }
  return errors;
}

/** 对象生命周期状态：0=未创建, 1=存活, 2=已删除 */
type ObjState = 0 | 1 | 2;

interface ObjInfo {
  name: string;
  createRank: number; // -1 表示该对象没有 create 事件（初始即存活）
  deleteRank: number; // -1 表示没有 delete 事件
}

export function solve(problem: Problem): SolveResult {
  const errors = validate(problem);
  if (errors.length > 0) {
    throw new Error("invalid problem: " + errors.join("; "));
  }

  const n = problem.events.length;
  const fullMask = (1 << n) - 1;
  const evOf = new Map(problem.events.map((e) => [e.id, e]));
  // 字典序按字符串（UTF-16 码元）顺序
  const sortedIds = [...evOf.keys()].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  const rankOf = new Map(sortedIds.map((id, i) => [id, i]));
  const byRank = sortedIds.map((id) => evOf.get(id)!);

  // 先后约束：predMask[r] 的位集合 = r 的所有直接前驱
  const predMask = new Array<number>(n).fill(0);
  const predIds = new Array<string[]>(n).fill(null).map(() => []);
  for (const [a, b] of problem.precedence ?? []) {
    const ra = rankOf.get(a)!;
    const rb = rankOf.get(b)!;
    predMask[rb] |= 1 << ra;
    predIds[rb].push(a);
  }

  const objInfo = new Map<string, ObjInfo>();
  for (let r = 0; r < n; r++) {
    const e = byRank[r];
    let o = objInfo.get(e.object);
    if (!o) {
      o = { name: e.object, createRank: -1, deleteRank: -1 };
      objInfo.set(e.object, o);
    }
    if (e.op === "create") o.createRank = r;
    if (e.op === "delete") o.deleteRank = r;
  }
  const objOfRank = byRank.map((e) => objInfo.get(e.object)!);
  const allObjects = [...objInfo.values()];
  const ranksOfObj = new Map<string, number[]>();
  for (let r = 0; r < n; r++) {
    const name = byRank[r].object;
    const arr = ranksOfObj.get(name) ?? [];
    arr.push(r);
    ranksOfObj.set(name, arr);
  }

  function stateOf(o: ObjInfo, mask: number): ObjState {
    if (o.deleteRank >= 0 && mask & (1 << o.deleteRank)) return 2;
    if (o.createRank < 0 || mask & (1 << o.createRank)) return 1;
    return 0;
  }

  function transitionOk(r: number, mask: number): boolean {
    const st = stateOf(objOfRank[r], mask);
    const op = byRank[r].op;
    if (op === "create") return st === 0;
    return st === 1; // update / delete 都要求对象存活
  }

  /** 局部合法：未放置、前驱已全部放置、状态迁移合法 */
  function locallyValid(r: number, mask: number): boolean {
    if (mask & (1 << r)) return false;
    if (predMask[r] & ~mask) return false;
    return transitionOk(r, mask);
  }

  // ---- 可行性预言机：剩余事件是否存在任一合法完成（DFS + 位掩码记忆化）----
  const infeasible = new Set<number>();
  function feasible(mask: number): boolean {
    if (mask === fullMask) return true;
    if (infeasible.has(mask)) return false;
    // 剪枝：对象已删除却还有未放置的事件 -> 必死
    for (const o of allObjects) {
      if (o.deleteRank >= 0 && mask & (1 << o.deleteRank)) {
        for (const r of ranksOfObj.get(o.name)!) {
          if (!(mask & (1 << r))) {
            infeasible.add(mask);
            return false;
          }
        }
      }
    }
    for (let r = 0; r < n; r++) {
      if (!locallyValid(r, mask)) continue;
      if (feasible(mask | (1 << r))) return true;
    }
    infeasible.add(mask);
    return false;
  }

  // ---- 先后约束环检测（用于无解报告）----
  function findCycle(): string[] | null {
    const adj = new Map<string, string[]>();
    for (const [a, b] of problem.precedence ?? []) {
      const arr = adj.get(a) ?? [];
      arr.push(b);
      adj.set(a, arr);
    }
    const color = new Map<string, number>(); // 0=未访问 1=在栈上 2=完成
    const stack: string[] = [];
    let cycle: string[] | null = null;
    function dfs(u: string): boolean {
      color.set(u, 1);
      stack.push(u);
      for (const v of adj.get(u) ?? []) {
        const c = color.get(v) ?? 0;
        if (c === 1) {
          cycle = stack.slice(stack.indexOf(v)).concat(v);
          return true;
        }
        if (c === 0 && dfs(v)) return true;
      }
      stack.pop();
      color.set(u, 2);
      return false;
    }
    for (const id of sortedIds) {
      if ((color.get(id) ?? 0) === 0 && dfs(id)) break;
    }
    return cycle;
  }

  function reasonsFor(r: number, mask: number): string[] {
    const e = byRank[r];
    const o = objOfRank[r];
    const st = stateOf(o, mask);
    const reasons: string[] = [];
    const waiting = predIds[r].filter((id) => !(mask & (1 << rankOf.get(id)!)));
    if (waiting.length > 0) {
      reasons.push(
        `precedence: requires ${waiting.map((w) => `'${w}'`).join(", ")} to be placed first`,
      );
    }
    const createId = o.createRank >= 0 ? byRank[o.createRank].id : null;
    const deleteId = o.deleteRank >= 0 ? byRank[o.deleteRank].id : null;
    if (e.op === "create" && st !== 0) {
      reasons.push(
        st === 1
          ? `state: object '${o.name}' is already created`
          : `state: object '${o.name}' is already deleted (by '${deleteId}')`,
      );
    } else if (e.op === "update" && st !== 1) {
      reasons.push(
        st === 0
          ? `state: object '${o.name}' is not created yet (create event '${createId}' has not been placed)`
          : `state: object '${o.name}' is already deleted (by '${deleteId}')`,
      );
    } else if (e.op === "delete" && st === 0) {
      reasons.push(
        `state: object '${o.name}' is not created yet (create event '${createId}' has not been placed)`,
      );
    }
    if (reasons.length === 0) {
      reasons.push("placing this event leads to a dead end (no valid completion)");
    }
    return reasons;
  }

  // ---- 有解：带预言机的字典序贪心，保证字典序最小 ----
  if (feasible(0)) {
    let mask = 0;
    const order: string[] = [];
    while (mask !== fullMask) {
      let chosen = -1;
      for (let r = 0; r < n; r++) {
        if (!locallyValid(r, mask)) continue;
        if (feasible(mask | (1 << r))) {
          chosen = r;
          break;
        }
      }
      if (chosen < 0) throw new Error("internal: feasible mask has no feasible successor");
      order.push(byRank[chosen].id);
      mask |= 1 << chosen;
    }
    return { status: "ok", order };
  }

  // ---- 无解：纯局部贪心走出字典序最小的极大局部合法前缀（阻塞前缀）----
  let mask = 0;
  const prefix: string[] = [];
  for (;;) {
    let chosen = -1;
    for (let r = 0; r < n; r++) {
      if (locallyValid(r, mask)) {
        chosen = r;
        break;
      }
    }
    if (chosen < 0) break;
    prefix.push(byRank[chosen].id);
    mask |= 1 << chosen;
  }
  const blocked: BlockedEvent[] = [];
  for (let r = 0; r < n; r++) {
    if (mask & (1 << r)) continue;
    blocked.push({ event: byRank[r].id, reasons: reasonsFor(r, mask) });
  }
  return { status: "unsat", blockingPrefix: prefix, blocked, cycle: findCycle() };
}
