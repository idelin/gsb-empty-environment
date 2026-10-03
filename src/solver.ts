/**
 * High-level scheduling solver.
 *
 * Combines the qualitative layer (Allen interval-algebra network tightened
 * by PC-2 path consistency) with a quantitative layer (STP over task start /
 * end points) to compute, for every task, its earliest / latest feasible
 * start and finish, its total float, and the project critical path window.
 *
 * Time is measured in abstract units from the project start (e.g. working
 * minutes); strict temporal orderings use `epsilon` as the minimum gap.
 */

import {
  UNIVERSAL,
  endpointConstraints,
  maskToString,
  parseRelations,
  relationsOf,
  type RelationSymbol,
} from './allen.ts';
import { TemporalNetwork } from './network.ts';
import { SimpleTemporalProblem } from './stp.ts';

export type TimePoint = 'start' | 'end';
export type ComparisonOp = '<' | '<=' | '=' | '>=' | '>';

export interface TaskWindow {
  task: string;
  earliestStart: number;
  latestStart: number;
  earliestFinish: number;
  latestFinish: number;
  /** Total float: how much the task can slip without moving the project. */
  float: number;
  critical: boolean;
}

export interface SolveResult {
  consistent: boolean;
  windows: TaskWindow[];
  /** Names of tasks on the critical path (minimum total float). */
  criticalTasks: string[];
  /** [start, end] span covered by the critical path, in solver time units. */
  criticalWindow: { start: number; end: number } | null;
  /** Overall project window. */
  projectWindow: { earliestStart: number; latestFinish: number } | null;
  /** Basic relations still allowed between two tasks after propagation. */
  relationBetween(a: number, b: number): RelationSymbol[];
  /** Same as relationBetween, formatted like "{b,m}". */
  relationLabel(a: number, b: number): string;
}

interface TaskDef {
  name: string;
  minDuration: number;
  maxDuration: number;
}

interface PointConstraint {
  task: number;
  point: TimePoint;
  op: ComparisonOp;
  value: number;
}

const FLOAT_TOLERANCE = 1e-9;

export class SchedulingSolver {
  /** Minimum gap used to turn strict orderings ("<") into "<=" bounds. */
  readonly epsilon: number;
  private tasks: TaskDef[] = [];
  private qualitative: Array<[number, number, number]> = [];
  private pointConstraints: PointConstraint[] = [];

  constructor(options: { epsilon?: number } = {}) {
    this.epsilon = options.epsilon ?? 1;
  }

  /** Add a task with a fixed duration or a {min, max} duration range. */
  addTask(name: string, duration: number | { min: number; max: number }): number {
    const d = typeof duration === 'number' ? { min: duration, max: duration } : duration;
    if (d.min < 0 || d.max < d.min) throw new Error(`Invalid duration for task ${name}`);
    this.tasks.push({ name, minDuration: d.min, maxDuration: d.max });
    return this.tasks.length - 1;
  }

  /**
   * Assert a qualitative constraint between two tasks, e.g.
   * constrain(a, b, 'before'), constrain(b, c, ['o']), constrain(a, b, '{b,m}').
   */
  constrain(a: number, b: number, relations: string | string[] | number): void {
    const mask = parseRelations(relations);
    if (mask === 0) throw new Error('Empty relation set');
    this.qualitative.push([a, b, mask]);
  }

  /**
   * Assert a point constraint against the project timeline, e.g.
   * require(c, 'start', '<', 5 * DAY)  — "C must start before day 5".
   */
  require(task: number, point: TimePoint, op: ComparisonOp, value: number): void {
    this.pointConstraints.push({ task, point, op, value });
  }

  taskCount(): number {
    return this.tasks.length;
  }

  solve(): SolveResult {
    const n = this.tasks.length;
    const inconsistent: SolveResult = {
      consistent: false,
      windows: [],
      criticalTasks: [],
      criticalWindow: null,
      projectWindow: null,
      relationBetween: () => [],
      relationLabel: () => '{}',
    };

    // --- Qualitative layer: transitive closure via path consistency. ---
    const network = new TemporalNetwork(n);
    for (const [i, j, mask] of this.qualitative) network.intersect(i, j, mask);
    if (!network.propagate()) return inconsistent;

    // --- Quantitative layer: STP over start/end points. ---
    const stp = new SimpleTemporalProblem();
    const startVar: number[] = [];
    const endVar: number[] = [];
    for (const task of this.tasks) {
      const s = stp.addVariable();
      const e = stp.addVariable();
      startVar.push(s);
      endVar.push(e);
      stp.addConstraint(s, e, task.maxDuration); // e - s <= maxDuration
      stp.addConstraint(e, s, -task.minDuration); // s - e <= -minDuration
      stp.addConstraint(s, stp.reference, 0); // start >= project start (0)
    }
    const pointVar = (task: number, slot: number): number =>
      slot % 2 === 0 ? startVar[task] : endVar[task];

    // Translate the *propagated* qualitative labels into endpoint bounds.
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const mask = network.mask(i, j);
        if (mask === UNIVERSAL) continue;
        for (const c of endpointConstraints(mask)) {
          // Endpoint slots: 0 = i.start, 1 = i.end, 2 = j.start, 3 = j.end.
          const from = pointVar(c.from < 2 ? i : j, c.from);
          const to = pointVar(c.to < 2 ? i : j, c.to);
          stp.addConstraint(from, to, c.maxLagUnits * this.epsilon);
        }
      }
    }

    for (const pc of this.pointConstraints) {
      const v = pc.point === 'start' ? startVar[pc.task] : endVar[pc.task];
      switch (pc.op) {
        case '<=':
          stp.addConstraint(stp.reference, v, pc.value);
          break;
        case '<':
          stp.addConstraint(stp.reference, v, pc.value - this.epsilon);
          break;
        case '>=':
          stp.addConstraint(v, stp.reference, -pc.value);
          break;
        case '>':
          stp.addConstraint(v, stp.reference, -(pc.value + this.epsilon));
          break;
        case '=':
          stp.addBounds(v, pc.value, pc.value);
          break;
      }
    }

    const solution = stp.solve();
    if (!solution.consistent) return inconsistent;

    const windows: TaskWindow[] = this.tasks.map((task, i) => ({
      task: task.name,
      earliestStart: solution.earliest[startVar[i]],
      latestStart: solution.latest[startVar[i]],
      earliestFinish: solution.earliest[endVar[i]],
      latestFinish: solution.latest[endVar[i]],
      float: solution.latest[startVar[i]] - solution.earliest[startVar[i]],
      critical: false,
    }));

    const minFloat = Math.min(...windows.map((w) => w.float));
    for (const w of windows) w.critical = w.float <= minFloat + FLOAT_TOLERANCE;
    const criticalWindows = windows.filter((w) => w.critical);

    return {
      consistent: true,
      windows,
      criticalTasks: criticalWindows.map((w) => w.task),
      criticalWindow: {
        start: Math.min(...criticalWindows.map((w) => w.earliestStart)),
        end: Math.max(...criticalWindows.map((w) => w.latestFinish)),
      },
      projectWindow: {
        earliestStart: Math.min(...windows.map((w) => w.earliestStart)),
        latestFinish: Math.max(...windows.map((w) => w.latestFinish)),
      },
      relationBetween: (a, b) => relationsOf(network.mask(a, b)),
      relationLabel: (a, b) => maskToString(network.mask(a, b)),
    };
  }
}
