import type { Scenario, Segment, TaskSpec, TaskState } from "./types.ts";

export interface BlockedInfo {
  id: string;
  lock: string;
  holder: string | null;
  waited: number;
  /** 距离超时还剩多少 tick；无超时为 null */
  timeoutIn: number | null;
}

export interface PrioInfo {
  id: string;
  eff: number;
  base: number;
}

export interface InheritInfo {
  id: string;
  from: string;
  lock: string;
  eff: number;
}

export interface TickRecord {
  time: number;
  /** 空闲快进时 >1，表示该记录覆盖的时间片数量 */
  duration: number;
  idle: boolean;
  running: PrioInfo | null;
  ready: PrioInfo[];
  blocked: BlockedInfo[];
  inherited: InheritInfo[];
  deadlockCycles: string[][];
  events: string[];
}

export interface TaskResult {
  id: string;
  state: TaskState;
  completedAt: number | null;
  aborted: boolean;
  basePriority: number;
}

export interface SimResult {
  name: string;
  status: "completed" | "deadlock" | "max-ticks-exceeded";
  ticks: TickRecord[];
  deadlocks: string[][];
  tasks: TaskResult[];
}

interface TaskRt {
  spec: TaskSpec;
  state: TaskState;
  seg: number;
  remaining: number;
  held: Set<string>;
  blockedOn: string | null;
  /** 在当前 acquire 片段上累计等待的 tick 数（被唤醒重试不清零） */
  waited: number;
  eff: number;
  inheritFrom: { from: string; lock: string } | null;
  completedAt: number | null;
  aborted: boolean;
}

export function simulate(scenario: Scenario): SimResult {
  const maxTicks = scenario.maxTicks ?? 10_000;
  const tasks = new Map<string, TaskRt>();
  for (const spec of scenario.tasks) {
    const first: Segment | undefined = spec.segments[0];
    tasks.set(spec.id, {
      spec,
      state: "pending",
      seg: 0,
      remaining: first && first.type === "compute" ? first.duration : 0,
      held: new Set(),
      blockedOn: null,
      waited: 0,
      eff: spec.priority,
      inheritFrom: null,
      completedAt: null,
      aborted: false,
    });
  }
  /** lock -> holder task id */
  const locks = new Map<string, string>();
  const ticks: TickRecord[] = [];
  const deadlocks: string[][] = [];
  const seenCycles = new Set<string>();
  let now = 0;
  let lastRunning: TaskRt | null = null;
  let status: SimResult["status"] = "completed";

  const segOf = (t: TaskRt): Segment | undefined => t.spec.segments[t.seg];
  const blockedTasks = (): TaskRt[] => [...tasks.values()].filter((t) => t.state === "blocked");
  const readyTasks = (): TaskRt[] => [...tasks.values()].filter((t) => t.state === "ready");

  function wakeWaiters(lock: string, events: string[]): void {
    for (const t of tasks.values()) {
      if (t.state === "blocked" && t.blockedOn === lock) {
        t.state = "ready";
        t.blockedOn = null;
        events.push(`${t.spec.id} unblocked (lock ${lock} released)`);
      }
    }
  }

  function releaseAll(t: TaskRt, events: string[]): void {
    for (const lock of [...t.held]) {
      t.held.delete(lock);
      locks.delete(lock);
      events.push(`${t.spec.id} released lock ${lock}`);
      wakeWaiters(lock, events);
    }
  }

  /**
   * 传递式优先级继承：被阻塞任务把有效优先级捐赠给锁持有者，
   * 持有者若也在等锁则继续向上捐赠，不动点迭代直到收敛。
   */
  function recomputeEffective(): void {
    for (const t of tasks.values()) {
      t.eff = t.spec.priority;
      t.inheritFrom = null;
    }
    let changed = true;
    while (changed) {
      changed = false;
      for (const b of blockedTasks()) {
        if (!b.blockedOn) continue;
        const holderId = locks.get(b.blockedOn);
        if (!holderId) continue;
        const h = tasks.get(holderId)!;
        if (b.eff > h.eff) {
          h.eff = b.eff;
          h.inheritFrom = { from: b.spec.id, lock: b.blockedOn };
          changed = true;
        }
      }
    }
  }

  /**
   * 等待环检测。每个被阻塞任务只有一条出边（等待的锁 -> 持有者），
   * 等待图是函数图，沿指针走即可找出所有环。
   */
  function findCycles(): string[][] {
    const edges = new Map<string, string>();
    for (const b of blockedTasks()) {
      if (!b.blockedOn) continue;
      const holder = locks.get(b.blockedOn);
      if (holder && holder !== b.spec.id) edges.set(b.spec.id, holder);
    }
    const cycles: string[][] = [];
    const done = new Set<string>();
    for (const start of edges.keys()) {
      if (done.has(start)) continue;
      const index = new Map<string, number>();
      const path: string[] = [];
      let cur: string | undefined = start;
      while (cur !== undefined && !done.has(cur)) {
        const at = index.get(cur);
        if (at !== undefined) {
          cycles.push([...path.slice(at), cur]);
          break;
        }
        index.set(cur, path.length);
        path.push(cur);
        cur = edges.get(cur);
      }
      for (const n of path) done.add(n);
    }
    return cycles;
  }

  function advance(t: TaskRt, events: string[]): void {
    t.seg += 1;
    t.waited = 0;
    if (t.seg >= t.spec.segments.length) {
      releaseAll(t, events);
      t.state = "done";
      t.blockedOn = null;
      t.completedAt = now;
      events.push(`${t.spec.id} completed`);
      return;
    }
    const s = segOf(t)!;
    if (s.type === "compute") t.remaining = s.duration;
  }

  function snapshotBlocked(): BlockedInfo[] {
    return blockedTasks().map((b) => {
      const s = segOf(b);
      const timeout = s && s.type === "acquire" && s.timeout !== undefined ? s.timeout : null;
      return {
        id: b.spec.id,
        lock: b.blockedOn ?? "?",
        holder: b.blockedOn ? locks.get(b.blockedOn) ?? null : null,
        waited: b.waited,
        timeoutIn: timeout === null ? null : Math.max(0, timeout - b.waited),
      };
    });
  }

  function snapshotReady(exclude?: string): PrioInfo[] {
    return readyTasks()
      .filter((t) => t.spec.id !== exclude)
      .map((t) => ({ id: t.spec.id, eff: t.eff, base: t.spec.priority }))
      .sort((a, b) => b.eff - a.eff || a.id.localeCompare(b.id));
  }

  function snapshotInherited(): InheritInfo[] {
    return [...tasks.values()]
      .filter((t) => t.eff > t.spec.priority && t.inheritFrom !== null)
      .map((t) => ({ id: t.spec.id, from: t.inheritFrom!.from, lock: t.inheritFrom!.lock, eff: t.eff }));
  }

  while (true) {
    if (now >= maxTicks) {
      status = "max-ticks-exceeded";
      break;
    }
    const events: string[] = [];

    // 1. 到达释放时间的任务进入就绪态
    for (const t of tasks.values()) {
      if (t.state === "pending" && (t.spec.releaseTime ?? 0) <= now) {
        t.state = "ready";
        events.push(`${t.spec.id} released (base priority ${t.spec.priority})`);
      }
    }

    // 2. 等待超时处理：abort（终止任务并释放其持有的锁）或 continue（跳过本次 acquire）
    for (const b of blockedTasks()) {
      const s = segOf(b);
      if (!s || s.type !== "acquire" || s.timeout === undefined) continue;
      if (b.waited >= s.timeout) {
        events.push(`${b.spec.id} timed out waiting for lock ${s.lock} (waited ${b.waited} tick(s))`);
        if ((s.onTimeout ?? "abort") === "abort") {
          b.aborted = true;
          b.state = "done";
          b.blockedOn = null;
          b.completedAt = now;
          releaseAll(b, events);
          events.push(`${b.spec.id} aborted`);
        } else {
          b.state = "ready";
          b.blockedOn = null;
          events.push(`${b.spec.id} skips acquire of ${s.lock} and continues`);
          advance(b, events);
        }
      }
    }

    // 3. 传递式优先级继承（每个 tick 重算，锁释放后自然回落到基础优先级）
    recomputeEffective();

    // 4. 锁等待环检测
    const cycles = findCycles();
    for (const c of cycles) {
      const key = c.slice(0, -1).sort().join("->");
      if (!seenCycles.has(key)) {
        seenCycles.add(key);
        deadlocks.push(c);
        events.push(`DEADLOCK detected: ${c.join(" -> ")}`);
      }
    }

    // 5. 抢占式调度：选有效优先级最高的就绪任务
    const chosen = readyTasks().sort(
      (a, b) =>
        b.eff - a.eff ||
        (a.spec.releaseTime ?? 0) - (b.spec.releaseTime ?? 0) ||
        a.spec.id.localeCompare(b.spec.id),
    )[0] ?? null;

    if (!chosen) {
      const pending = [...tasks.values()].filter((t) => t.state === "pending");
      const blocked = blockedTasks();
      if (pending.length === 0 && blocked.length === 0) {
        status = "completed";
        break;
      }
      // 计算下一个可能改变状态的时刻：下一个任务释放 / 下一个等待超时
      let next = Infinity;
      for (const t of pending) next = Math.min(next, t.spec.releaseTime ?? 0);
      for (const b of blocked) {
        const s = segOf(b);
        if (s && s.type === "acquire" && s.timeout !== undefined) {
          next = Math.min(next, now + Math.max(1, s.timeout - b.waited));
        }
      }
      if (!Number.isFinite(next)) {
        // 没有就绪任务、没有未来释放、没有任何超时可期待：永久死锁，而不是普通空闲
        ticks.push({
          time: now,
          duration: 0,
          idle: false,
          running: null,
          ready: [],
          blocked: snapshotBlocked(),
          inherited: snapshotInherited(),
          deadlockCycles: cycles,
          events: [...events, "permanent deadlock: no runnable task, no future release, no active timeout"],
        });
        status = "deadlock";
        break;
      }
      const jump = Math.max(1, next - now);
      for (const b of blocked) b.waited += jump;
      ticks.push({
        time: now,
        duration: jump,
        idle: true,
        running: null,
        ready: [],
        blocked: snapshotBlocked(),
        inherited: snapshotInherited(),
        deadlockCycles: cycles,
        events: [...events, `idle ${jump} tick(s): no runnable task (fast-forward)`],
      });
      now += jump;
      lastRunning = null;
      continue;
    }

    if (lastRunning && lastRunning.state === "ready" && lastRunning !== chosen) {
      events.push(
        `${chosen.spec.id} preempts ${lastRunning.spec.id} (eff ${chosen.eff} vs ${lastRunning.eff})`,
      );
    }

    // 6. 运行选中的任务一个 tick。
    //    acquire/release 是瞬时动作，在同一 tick 内连续处理；
    //    每个 tick 最多消耗 1 单位 compute；acquire 失败则任务阻塞。
    let guard = 0;
    let consumedCompute = false;
    while (guard++ < 10_000) {
      const s = segOf(chosen);
      if (!s) break;
      if (s.type === "compute") {
        if (consumedCompute) break;
        if (chosen.remaining <= 0) {
          advance(chosen, events);
          continue;
        }
        chosen.remaining -= 1;
        consumedCompute = true;
        if (chosen.remaining === 0) {
          advance(chosen, events);
          continue;
        }
        break;
      }
      if (s.type === "acquire") {
        const holder = locks.get(s.lock);
        if (holder === undefined) {
          locks.set(s.lock, chosen.spec.id);
          chosen.held.add(s.lock);
          events.push(`${chosen.spec.id} acquired lock ${s.lock}`);
          advance(chosen, events);
          continue;
        }
        if (holder === chosen.spec.id) {
          events.push(`${chosen.spec.id} already holds lock ${s.lock} (re-entrant acquire ignored)`);
          advance(chosen, events);
          continue;
        }
        chosen.state = "blocked";
        chosen.blockedOn = s.lock;
        events.push(`${chosen.spec.id} blocked on lock ${s.lock} held by ${holder}`);
        break;
      }
      // release
      if (chosen.held.has(s.lock)) {
        chosen.held.delete(s.lock);
        locks.delete(s.lock);
        events.push(`${chosen.spec.id} released lock ${s.lock}`);
        wakeWaiters(s.lock, events);
      } else {
        events.push(`${chosen.spec.id} does not hold lock ${s.lock} (release ignored)`);
      }
      advance(chosen, events);
    }

    // 7. 本 tick 结束：所有阻塞任务累计等待时间
    for (const b of blockedTasks()) b.waited += 1;

    // 8. 锁释放后重算有效优先级，保证本 tick 的快照反映最新继承关系
    recomputeEffective();

    ticks.push({
      time: now,
      duration: 1,
      idle: false,
      running: { id: chosen.spec.id, eff: chosen.eff, base: chosen.spec.priority },
      ready: snapshotReady(chosen.spec.id),
      blocked: snapshotBlocked(),
      inherited: snapshotInherited(),
      deadlockCycles: cycles,
      events,
    });
    lastRunning = chosen;
    now += 1;
  }

  return {
    name: scenario.name ?? "scenario",
    status,
    ticks,
    deadlocks,
    tasks: [...tasks.values()].map((t) => ({
      id: t.spec.id,
      state: t.state,
      completedAt: t.completedAt,
      aborted: t.aborted,
      basePriority: t.spec.priority,
    })),
  };
}
