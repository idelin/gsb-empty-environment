import {
  Scenario,
  TaskSpec,
  TaskStatus,
  TickRecord,
  BlockedInfo,
  TaskSummary,
  SimulationResult,
} from './types';

interface TaskState {
  spec: TaskSpec;
  order: number;
  status: TaskStatus;
  effectivePriority: number;
  segmentIndex: number;
  segmentRemaining: number;
  heldLocks: string[];
  waitingOn: string | null;
  waitStart: number;
  finishTick: number | null;
}

const TERMINAL_STATUSES: TaskStatus[] = ['done', 'timed_out', 'deadlocked'];

export class Scheduler {
  private tasks: TaskState[] = [];
  private byId = new Map<string, TaskState>();
  private holder = new Map<string, string | null>();
  private maxTicks: number;
  private lastRunning: string | null = null;

  constructor(private scenario: Scenario) {
    this.maxTicks = scenario.maxTicks !== undefined ? scenario.maxTicks : 10000;
    const seen = new Set<string>();
    scenario.tasks.forEach((spec, order) => {
      if (seen.has(spec.id)) {
        throw new Error(`duplicate task id: ${spec.id}`);
      }
      seen.add(spec.id);
      for (const seg of spec.segments) {
        if (seg.kind === 'run' && (!Number.isInteger(seg.duration) || seg.duration < 1)) {
          throw new Error(`task ${spec.id}: run duration must be a positive integer`);
        }
      }
      const state: TaskState = {
        spec,
        order,
        status: 'pending',
        effectivePriority: spec.priority,
        segmentIndex: 0,
        segmentRemaining: 0,
        heldLocks: [],
        waitingOn: null,
        waitStart: 0,
        finishTick: null,
      };
      this.tasks.push(state);
      this.byId.set(spec.id, state);
    });
  }

  run(): SimulationResult {
    const ticks: TickRecord[] = [];
    for (let t = 0; t <= this.maxTicks; t++) {
      const events: string[] = [];
      this.arrive(t, events);
      this.checkTimeouts(t, events);
      this.recomputeEffective();
      this.detectDeadlock(t, events);
      if (this.allTerminal()) {
        if (events.length > 0) {
          ticks.push(this.record(t, null, events));
        }
        break;
      }
      const running = this.runOneTick(t, events);
      this.recomputeEffective();
      this.detectDeadlock(t, events);
      ticks.push(this.record(t, running, events));
      if (this.allTerminal()) {
        break;
      }
    }
    return { name: this.scenario.name, ticks, tasks: this.summaries() };
  }

  private arrive(t: number, events: string[]): void {
    for (const task of this.tasks) {
      if (task.status === 'pending' && task.spec.releaseTime <= t) {
        task.status = 'ready';
        events.push(`${task.spec.id} arrives (base priority ${task.spec.priority})`);
      }
    }
  }

  private checkTimeouts(t: number, events: string[]): void {
    for (const task of this.tasks) {
      if (task.status !== 'blocked') continue;
      const timeout = task.spec.lockTimeout;
      if (timeout === undefined) continue;
      if (t - task.waitStart >= timeout) {
        events.push(
          `${task.spec.id} timed out waiting for ${task.waitingOn} ` +
            `(waited ${t - task.waitStart} ticks, timeout ${timeout})`
        );
        this.terminate(task, t, events, 'timed_out');
      }
    }
  }

  // Transitive priority inheritance: a lock holder inherits the highest
  // effective priority among all (direct and indirect) waiters. Computed as
  // a fixpoint over the wait-for graph, so chains of any depth propagate and
  // cycles cannot cause non-termination (values only increase, bounded above).
  private recomputeEffective(): void {
    for (const task of this.tasks) {
      task.effectivePriority = task.spec.priority;
    }
    for (let iter = 0; iter <= this.tasks.length; iter++) {
      let changed = false;
      for (const task of this.tasks) {
        if (task.status !== 'blocked' || task.waitingOn === null) continue;
        const holderId = this.holder.get(task.waitingOn);
        if (!holderId) continue;
        const holderTask = this.byId.get(holderId)!;
        if (this.isTerminal(holderTask)) continue;
        if (task.effectivePriority > holderTask.effectivePriority) {
          holderTask.effectivePriority = task.effectivePriority;
          changed = true;
        }
      }
      if (!changed) break;
    }
  }

  // Wait-for cycle detection: edge waiter -> holder for every blocked task.
  private detectDeadlock(t: number, events: string[]): void {
    const edge = new Map<string, string>();
    for (const task of this.tasks) {
      if (task.status === 'blocked' && task.waitingOn !== null) {
        const holderId = this.holder.get(task.waitingOn);
        if (holderId) {
          const holderTask = this.byId.get(holderId)!;
          if (!this.isTerminal(holderTask)) {
            edge.set(task.spec.id, holderId);
          }
        }
      }
    }
    const visited = new Set<string>();
    for (const start of Array.from(edge.keys())) {
      if (visited.has(start)) continue;
      const path: string[] = [];
      const pos = new Map<string, number>();
      let cur: string | undefined = start;
      while (cur !== undefined && edge.has(cur) && !pos.has(cur)) {
        pos.set(cur, path.length);
        path.push(cur);
        cur = edge.get(cur);
      }
      for (const id of path) visited.add(id);
      if (cur !== undefined && pos.has(cur)) {
        const cycle = path.slice(pos.get(cur)!);
        cycle.push(cur);
        events.push(`DEADLOCK: lock wait cycle detected: ${cycle.join(' -> ')}`);
        const members = new Set(cycle);
        for (const id of Array.from(members)) {
          const task = this.byId.get(id)!;
          if (!this.isTerminal(task)) {
            this.terminate(task, t, events, 'deadlocked');
          }
        }
      }
    }
  }

  private runOneTick(t: number, events: string[]): string | null {
    const skipped = new Set<string>();
    for (;;) {
      const task = this.pick(skipped);
      if (!task) return null;
      const outcome = this.processInstantSegments(task, t, events);
      if (outcome === 'blocked' || outcome === 'done') {
        skipped.add(task.spec.id);
        if (outcome === 'blocked') {
          // Boost the holder immediately so it can be picked within this tick.
          this.recomputeEffective();
        }
        continue;
      }
      if (outcome === 'reschedule') {
        // A lock was released: effective priorities must be recomputed
        // before deciding who gets the CPU.
        this.recomputeEffective();
        continue;
      }
      // outcome === 'run': the task consumes this tick.
      if (this.lastRunning !== null && this.lastRunning !== task.spec.id) {
        const prev = this.byId.get(this.lastRunning)!;
        if (prev.status === 'ready') {
          events.push(
            `${task.spec.id} preempts ${prev.spec.id} ` +
              `(eff ${task.effectivePriority} vs ${prev.effectivePriority})`
          );
        }
      }
      task.status = 'running';
      task.segmentRemaining -= 1;
      if (task.segmentRemaining === 0) {
        task.segmentIndex += 1;
      }
      this.lastRunning = task.spec.id;
      if (task.segmentIndex >= task.spec.segments.length) {
        this.terminate(task, t, events, 'done');
        this.lastRunning = null;
      } else {
        task.status = 'ready';
      }
      return task.spec.id;
    }
  }

  private processInstantSegments(
    task: TaskState,
    t: number,
    events: string[]
  ): 'run' | 'blocked' | 'done' | 'reschedule' {
    let releasedLock = false;
    for (;;) {
      if (task.segmentIndex >= task.spec.segments.length) {
        this.terminate(task, t, events, 'done');
        return 'done';
      }
      const seg = task.spec.segments[task.segmentIndex];
      if (seg.kind === 'run') {
        if (task.segmentRemaining === 0) {
          task.segmentRemaining = seg.duration;
        }
        return releasedLock ? 'reschedule' : 'run';
      }
      if (seg.kind === 'unlock') {
        task.segmentIndex += 1;
        if (this.holder.get(seg.lock) !== task.spec.id) {
          events.push(`${task.spec.id} unlocks ${seg.lock} it does not hold (ignored)`);
          continue;
        }
        this.releaseLock(task, seg.lock, events);
        releasedLock = true;
        continue;
      }
      // seg.kind === 'lock'
      const holderId = this.holder.get(seg.lock) || null;
      if (holderId === null) {
        this.holder.set(seg.lock, task.spec.id);
        task.heldLocks.push(seg.lock);
        task.segmentIndex += 1;
        events.push(`${task.spec.id} acquires ${seg.lock}`);
        continue;
      }
      if (holderId === task.spec.id) {
        events.push(`${task.spec.id} re-locks ${seg.lock} it already holds (ignored)`);
        task.segmentIndex += 1;
        continue;
      }
      task.status = 'blocked';
      task.waitingOn = seg.lock;
      task.waitStart = t;
      const holderTask = this.byId.get(holderId)!;
      events.push(
        `${task.spec.id} blocks on ${seg.lock} ` +
          `(held by ${holderId}, holder eff priority ${holderTask.effectivePriority})`
      );
      return 'blocked';
    }
  }

  private releaseLock(task: TaskState, lock: string, events: string[]): void {
    this.holder.set(lock, null);
    task.heldLocks = task.heldLocks.filter((l) => l !== lock);
    events.push(`${task.spec.id} releases ${lock} -> effective priorities recomputed`);
    for (const waiter of this.tasks) {
      if (waiter.status === 'blocked' && waiter.waitingOn === lock) {
        waiter.status = 'ready';
        waiter.waitingOn = null;
        events.push(`${waiter.spec.id} wakes up (${lock} released)`);
      }
    }
  }

  private terminate(
    task: TaskState,
    t: number,
    events: string[],
    status: 'done' | 'timed_out' | 'deadlocked'
  ): void {
    task.status = status;
    task.waitingOn = null;
    task.finishTick = t;
    events.push(`${task.spec.id} ${status === 'done' ? 'finishes' : 'exits'} (status: ${status})`);
    for (const lock of task.heldLocks.slice()) {
      events.push(`warning: ${lock} auto-released on ${status}`);
      this.releaseLock(task, lock, events);
    }
  }

  private pick(skipped: Set<string>): TaskState | null {
    let best: TaskState | null = null;
    for (const task of this.tasks) {
      if (task.status !== 'ready' || skipped.has(task.spec.id)) continue;
      if (
        best === null ||
        task.effectivePriority > best.effectivePriority ||
        (task.effectivePriority === best.effectivePriority &&
          (task.spec.releaseTime < best.spec.releaseTime ||
            (task.spec.releaseTime === best.spec.releaseTime && task.order < best.order)))
      ) {
        best = task;
      }
    }
    return best;
  }

  private record(t: number, running: string | null, events: string[]): TickRecord {
    const blocked: BlockedInfo[] = [];
    for (const task of this.tasks) {
      if (task.status === 'blocked' && task.waitingOn !== null) {
        blocked.push({
          id: task.spec.id,
          lock: task.waitingOn,
          holder: this.holder.get(task.waitingOn) || null,
          waited: t - task.waitStart,
          timeout: task.spec.lockTimeout,
        });
      }
    }
    const runningTask = running !== null ? this.byId.get(running)! : null;
    return {
      tick: t,
      running,
      basePriority: runningTask ? runningTask.spec.priority : null,
      effectivePriority: runningTask ? runningTask.effectivePriority : null,
      events,
      blocked,
    };
  }

  private isTerminal(task: TaskState): boolean {
    return TERMINAL_STATUSES.indexOf(task.status) >= 0;
  }

  private allTerminal(): boolean {
    return this.tasks.every((task) => this.isTerminal(task));
  }

  private summaries(): TaskSummary[] {
    return this.tasks.map((task) => ({
      id: task.spec.id,
      status: task.status,
      finishTick: task.finishTick,
    }));
  }
}
