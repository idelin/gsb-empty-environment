/**
 * Temporal scheduling solver.
 *
 * Two cooperating layers:
 *  1. a qualitative Allen-algebra network (path consistency / transitive
 *     closure over the 13 basic relations), and
 *  2. a metric Simple Temporal Network over interval endpoints, durations and
 *     deadlines, which yields earliest/latest feasible windows per task.
 *
 * Qualitative edges are injected into the STN through their convex hull over
 * endpoint orderings: a bound x - y <= c is kept only when EVERY disjunct of
 * the (possibly disjunctive) relation implies it, so the metric relaxation is
 * always sound.  Strict orderings are modelled with a unit gap of one day
 * (integer-day granularity).
 */

import {
  relationSetOf,
  singleton,
  toArray,
  UNIVERSAL,
  type BasicRelation,
  type RelationSet,
} from './allen.ts';
import { AllenNetwork } from './network.ts';
import { SimpleTemporalNetwork } from './stn.ts';

export type RelationName =
  | 'before' | 'meets' | 'overlaps' | 'starts' | 'during' | 'finishes'
  | 'equals' | 'after' | 'met-by' | 'overlapped-by' | 'started-by'
  | 'contains' | 'finished-by';

const NAME_TO_BASIC: Record<RelationName, BasicRelation> = {
  before: 'b', meets: 'm', overlaps: 'o', starts: 's', during: 'd',
  finishes: 'f', equals: 'eq', after: 'bi', 'met-by': 'mi',
  'overlapped-by': 'oi', 'started-by': 'si', contains: 'di',
  'finished-by': 'fi',
};

/** Unit gap used to model strict inequalities at integer-day granularity. */
export const STRICT_GAP = 1;

// Endpoint indices inside a qualitative edge: A.start, A.end, B.start, B.end.
const AS = 0;
const AE = 1;
const BS = 2;
const BE = 3;

type EndpointConstraint = [x: number, y: number, c: number]; // x - y <= c

const ENDPOINT_CONSTRAINTS: Record<BasicRelation, EndpointConstraint[]> = {
  b:  [[AE, BS, 0]],
  m:  [[AE, BS, 0], [BS, AE, 0]],
  o:  [[AS, BS, -STRICT_GAP], [BS, AE, -STRICT_GAP], [AE, BE, -STRICT_GAP]],
  s:  [[AS, BS, 0], [BS, AS, 0], [AE, BE, -STRICT_GAP]],
  d:  [[BS, AS, -STRICT_GAP], [AE, BE, -STRICT_GAP]],
  f:  [[BS, AS, -STRICT_GAP], [AE, BE, 0], [BE, AE, 0]],
  eq: [[AS, BS, 0], [BS, AS, 0], [AE, BE, 0], [BE, AE, 0]],
  bi: [[BE, AS, 0]],
  mi: [[BE, AS, 0], [AS, BE, 0]],
  oi: [[BS, AS, -STRICT_GAP], [AS, BE, -STRICT_GAP], [BE, AE, -STRICT_GAP]],
  si: [[AS, BS, 0], [BS, AS, 0], [BE, AE, -STRICT_GAP]],
  di: [[AS, BS, -STRICT_GAP], [BE, AE, -STRICT_GAP]],
  fi: [[AS, BS, -STRICT_GAP], [AE, BE, 0], [BE, AE, 0]],
};

/**
 * Convex hull of a relation set over endpoint orderings: keeps exactly the
 * bounds x - y <= c that hold for every disjunct.
 */
export function hullConstraints(set: RelationSet): EndpointConstraint[] {
  const rels = toArray(set);
  const out: EndpointConstraint[] = [];
  const endpoints = [AS, AE, BS, BE];
  for (const x of endpoints) {
    for (const y of endpoints) {
      if (x === y) continue;
      let bound: number | null = null;
      for (const rel of rels) {
        let tightest: number | null = null;
        for (const [cx, cy, cc] of ENDPOINT_CONSTRAINTS[rel]) {
          if (cx === x && cy === y && (tightest === null || cc < tightest)) {
            tightest = cc;
          }
        }
        if (tightest === null) {
          bound = null;
          break;
        }
        bound = bound === null ? tightest : Math.max(bound, tightest);
      }
      if (bound !== null) out.push([x, y, bound]);
    }
  }
  return out;
}

export interface Duration {
  min: number;
  max: number;
}

export interface TaskWindow {
  name: string;
  earliestStart: number;
  latestStart: number;
  earliestEnd: number;
  latestEnd: number;
  /** Total float on the start time: latestStart - earliestStart. */
  float: number;
}

export interface SolveResult {
  consistent: boolean;
  /** False when the qualitative Allen network alone is inconsistent. */
  qualitativelyConsistent: boolean;
  tasks: TaskWindow[];
  /** [earliest, latest] feasible project completion day. */
  projectWindow: [number, number] | null;
  /** Names of tasks with zero-free slack relative to the tightest float. */
  criticalPath: string[];
  /** Tightened qualitative relation between every pair of tasks. */
  relationBetween: (a: string, b: string) => RelationSet;
}

interface MetricConstraint {
  task: string;
  point: 'start' | 'end';
  earliest?: number;
  latest?: number;
}

export class Scheduler {
  private readonly taskNames: string[] = [];
  private readonly durations: Duration[] = [];
  private readonly taskIndex = new Map<string, number>();
  private readonly relations: { a: number; b: number; set: RelationSet }[] = [];
  private readonly metric: MetricConstraint[] = [];

  addTask(name: string, duration: number | Duration): this {
    if (this.taskIndex.has(name)) throw new Error(`duplicate task ${name}`);
    const d = typeof duration === 'number'
      ? { min: duration, max: duration }
      : duration;
    if (d.min <= 0 || d.max < d.min) {
      throw new Error(`invalid duration for ${name}`);
    }
    this.taskIndex.set(name, this.taskNames.length);
    this.taskNames.push(name);
    this.durations.push(d);
    return this;
  }

  /** Add a qualitative constraint: A <relation(s)> B. */
  addRelation(a: string, relation: RelationName | RelationName[], b: string): this {
    const names = Array.isArray(relation) ? relation : [relation];
    const set = relationSetOf(...names.map((n) => NAME_TO_BASIC[n]));
    this.relations.push({ a: this.idx(a), b: this.idx(b), set });
    return this;
  }

  /** Task must start strictly before `day` (start <= day - 1, integer days). */
  startBefore(task: string, day: number): this {
    this.metric.push({ task, point: 'start', latest: day - STRICT_GAP });
    return this;
  }

  /** Task must start on or before `day`. */
  startOnOrBefore(task: string, day: number): this {
    this.metric.push({ task, point: 'start', latest: day });
    return this;
  }

  /** Task must end on or before `day`. */
  endOnOrBefore(task: string, day: number): this {
    this.metric.push({ task, point: 'end', latest: day });
    return this;
  }

  /** Task cannot start before `day` (release date). */
  releaseOn(task: string, day: number): this {
    this.metric.push({ task, point: 'start', earliest: day });
    return this;
  }

  private idx(name: string): number {
    const i = this.taskIndex.get(name);
    if (i === undefined) throw new Error(`unknown task ${name}`);
    return i;
  }

  solve(): SolveResult {
    const n = this.taskNames.length;

    // --- Layer 1: qualitative path consistency (transitive closure). ---
    const network = new AllenNetwork(n);
    let qualitativelyConsistent = true;
    for (const { a, b, set } of this.relations) {
      if (!network.constrain(a, b, set)) {
        qualitativelyConsistent = false;
        break;
      }
    }
    if (qualitativelyConsistent && !network.propagateAll()) {
      qualitativelyConsistent = false;
    }
    if (!qualitativelyConsistent) {
      return {
        consistent: false,
        qualitativelyConsistent: false,
        tasks: [],
        projectWindow: null,
        criticalPath: [],
        relationBetween: () => 0,
      };
    }

    // --- Layer 2: metric STN over endpoints. ---
    // Node layout: 0 = time zero, 1+2i = start_i, 2+2i = end_i.
    const ZERO = 0;
    const startNode = (i: number) => 1 + 2 * i;
    const endNode = (i: number) => 2 + 2 * i;
    const stn = new SimpleTemporalNetwork(2 * n + 1);

    for (let i = 0; i < n; i++) {
      const s = startNode(i);
      const e = endNode(i);
      const { min, max } = this.durations[i];
      stn.addConstraint(s, e, -min); // end - start >= min
      stn.addConstraint(e, s, max); //  end - start <= max
      stn.addConstraint(ZERO, s, 0); // start >= time zero
    }

    for (const mc of this.metric) {
      const i = this.idx(mc.task);
      const node = mc.point === 'start' ? startNode(i) : endNode(i);
      if (mc.latest !== undefined) stn.addConstraint(node, ZERO, mc.latest);
      if (mc.earliest !== undefined) stn.addConstraint(ZERO, node, -mc.earliest);
    }

    // Inject the tightened qualitative edges via their convex hull.
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const set = network.get(i, j);
        if (set === UNIVERSAL) continue;
        const nodes = [startNode(i), endNode(i), startNode(j), endNode(j)];
        for (const [x, y, c] of hullConstraints(set)) {
          stn.addConstraint(nodes[x], nodes[y], c);
        }
      }
    }

    if (!stn.close()) {
      return {
        consistent: false,
        qualitativelyConsistent: true,
        tasks: [],
        projectWindow: null,
        criticalPath: [],
        relationBetween: (a, b) => network.get(this.idx(a), this.idx(b)),
      };
    }

    const tasks: TaskWindow[] = this.taskNames.map((name, i) => {
      const es = stn.earliest(startNode(i));
      const ls = stn.latest(startNode(i));
      const ee = stn.earliest(endNode(i));
      const le = stn.latest(endNode(i));
      return {
        name,
        earliestStart: es,
        latestStart: ls,
        earliestEnd: ee,
        latestEnd: le,
        float: ls - es,
      };
    });

    const minFloat = Math.min(...tasks.map((t) => t.float));
    const criticalPath = tasks
      .filter((t) => t.float === minFloat)
      .map((t) => t.name);

    return {
      consistent: true,
      qualitativelyConsistent: true,
      tasks,
      // The project completes when every task completes.
      projectWindow: [
        Math.max(...tasks.map((t) => t.earliestEnd)),
        Math.max(...tasks.map((t) => t.latestEnd)),
      ],
      criticalPath,
      relationBetween: (a, b) => network.get(this.idx(a), this.idx(b)),
    };
  }
}
