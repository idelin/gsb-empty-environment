import { Rational, rat } from "./rational.ts";
import type { RationalInput } from "./rational.ts";
import { deviceKey, sameDevice } from "./model.ts";
import type {
  ClockObservation,
  DeviceClock,
  DeviceIdentity,
} from "./model.ts";
import { rationalRange } from "./time.ts";
import type { RationalRange } from "./time.ts";

/**
 * Per-device clock constraints.
 *
 * Each node is a crossing of a known local-clock value L.  Its server time is
 * T, and offset(L) = L - T is bounded.  For two ordered local values Li <= Lj,
 * bounded drift gives
 *
 *   (Lj - Li) / (1 + driftHi) <= Tj - Ti <= (Lj - Li) / (1 + driftLo).
 *
 * Observation (server S, local [a,b]) is encoded by virtual crossings a and b
 * with Ta <= S <= Tb.  All inequalities share one graph, so the solver never
 * stitches together incompatible clock readings from different observations.
 */

export interface LocalEvent {
  local: Rational;
  source: string;
}

export interface ClockSystemBuild {
  device: DeviceIdentity;
  key: string;
  offset: RationalRange;
  drift: RationalRange;
  nodes: LocalEvent[];
  min: Rational[];
  max: Rational[];
  feasible: boolean;
}

export interface ClockWitnessEvent {
  local: Rational;
  server: Rational;
  source: string;
}

export interface ClockWitness {
  deviceKey: string;
  deviceId: string;
  generation: string;
  offset: { lo: Rational; hi: Rational };
  drift: { lo: Rational; hi: Rational };
  events: ClockWitnessEvent[];
}

interface Edge {
  from: number;
  to: number;
  weight: Rational;
}

const ZERO = Rational.zero();

function localKey(value: Rational): string {
  return `${value.n}/${value.d}`;
}

export class ClockConstraintError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ClockConstraintError";
  }
}

export class ClockSolver {
  private readonly systems = new Map<string, ClockSystemBuild>();

  constructor(clocks: DeviceClock[], extraObservations: ClockObservation[] = []) {
    for (const clock of clocks) {
      const key = deviceKey(clock.device);
      if (this.systems.has(key)) {
        throw new ClockConstraintError(`duplicate clock profile for ${key}`);
      }
      const offset = rationalRange(clock.offset);
      const drift = rationalRange(clock.drift);
      if (offset.loInclusive === false || offset.hiInclusive === false) {
        // The constraint graph uses closed rational bounds.  Open clock bounds
        // cannot enlarge a lease's possible lifetime, so conservatively widening
        // to the closure is safe.
      }
      if (rat(1).add(drift.lo).lte(0)) {
        throw new ClockConstraintError(
          `device ${key}: clock must be monotone (1 + driftLo > 0)`,
        );
      }
      this.systems.set(key, {
        device: clock.device,
        key,
        offset,
        drift,
        nodes: [],
        min: [],
        max: [],
        feasible: false,
      });
    }

    const observationMap = new Map<string, ClockObservation[]>();
    for (const clock of clocks) {
      if (clock.observations) {
        for (const observation of clock.observations) {
          this.addObservation(observationMap, observation, clock.device);
        }
      }
    }
    for (const observation of extraObservations) {
      this.addObservation(observationMap, observation);
    }

    for (const [key, observations] of observationMap) {
      if (!this.systems.has(key)) {
        throw new ClockConstraintError(
          `observation references device ${key} without a clock profile`,
        );
      }
      const system = this.systems.get(key)!;
      for (const observation of observations) {
        const server = rat(observation.serverTime);
        const lo = rat(observation.deviceTime.lo);
        const hi = rat(observation.deviceTime.hi);
        if (lo.gt(hi)) {
          throw new ClockConstraintError(
            `device ${key}: observation interval has lo > hi`,
          );
        }
        const nodeLo = this.nodeIndex(system, lo, "observation");
        const nodeHi = this.nodeIndex(system, hi, "observation");
        this.addSystemConstraint(system, { lo: null, hi: server }, nodeLo);
        this.addSystemConstraint(system, { lo: server, hi: null }, nodeHi);
        if (lo.eq(hi)) {
          this.addSystemConstraint(system, { lo: server, hi: server }, nodeLo);
        }
      }
    }
  }

  private addObservation(
    map: Map<string, ClockObservation[]>,
    observation: ClockObservation,
    expected?: DeviceIdentity,
  ): void {
    const key = deviceKey(observation.device);
    if (expected && !sameDevice(expected, observation.device)) {
      throw new ClockConstraintError(
        `observation device ${key} does not match clock device ${deviceKey(expected)}`,
      );
    }
    const list = map.get(key) ?? [];
    list.push(observation);
    map.set(key, list);
  }

  getSystem(device: DeviceIdentity): ClockSystemBuild {
    const key = deviceKey(device);
    const system = this.systems.get(key);
    if (!system) {
      // An unspecified clock is an exact server clock.  This keeps small
      // examples terse while still making the choice explicit in certificates.
      const fallback: ClockSystemBuild = {
        device,
        key,
        offset: rationalRange({ lo: 0, hi: 0 }),
        drift: rationalRange({ lo: 0, hi: 0 }),
        nodes: [],
        min: [],
        max: [],
        feasible: false,
      };
      this.systems.set(key, fallback);
      return fallback;
    }
    return system;
  }

  private nodeIndex(system: ClockSystemBuild, local: Rational, source: string): number {
    const key = localKey(local);
    for (let i = 0; i < system.nodes.length; i++) {
      if (localKey(system.nodes[i].local) === key) return i;
    }
    system.nodes.push({ local, source });
    return system.nodes.length - 1;
  }

  /** Add an absolute (upper/lower) bound for one node. */
  private addSystemConstraint(
    system: ClockSystemBuild,
    bound: { lo: Rational | null; hi: Rational | null },
    node: number,
  ): void {
    const constraints = (system as ClockSystemBuild & {
      absolute?: Array<{ node: number; lo: Rational | null; hi: Rational | null }>;
    }).absolute ?? [];
    constraints.push({ node, ...bound });
    (system as ClockSystemBuild & { absolute?: typeof constraints }).absolute = constraints;
  }

  /** Ensure a local event node exists and solve all systems. */
  prepare(events: Map<string, LocalEvent[]>): void {
    for (const [key, system] of this.systems) {
      const fromEvents = events.get(key) ?? [];
      for (const event of fromEvents) this.nodeIndex(system, event.local, event.source);
    }
    for (const system of this.systems.values()) this.solveSystem(system);
  }

  private solveSystem(system: ClockSystemBuild): void {
    const n = system.nodes.length;
    if (n === 0) {
      system.feasible = true;
      system.min = [];
      system.max = [];
      return;
    }

    const z = n;
    const edges: Edge[] = [];
    const add = (from: number, to: number, weight: Rational): void => {
      edges.push({ from, to, weight });
    };

    const offsetLo = system.offset.lo;
    const offsetHi = system.offset.hi;
    const driftLo = system.drift.lo;
    const driftHi = system.drift.hi;
    const onePlusLo = rat(1).add(driftLo);
    const onePlusHi = rat(1).add(driftHi);

    for (let i = 0; i < n; i++) {
      const local = system.nodes[i].local;
      // local - offsetHi <= T <= local - offsetLo
      add(z, i, local.sub(offsetLo));
      add(i, z, local.sub(offsetHi).neg());
    }

    const absolute = (system as ClockSystemBuild & {
      absolute?: Array<{ node: number; lo: Rational | null; hi: Rational | null }>;
    }).absolute;
    for (const bound of absolute ?? []) {
      if (bound.hi !== null) add(z, bound.node, bound.hi);
      if (bound.lo !== null) add(bound.node, z, bound.lo.neg());
    }

    for (let i = 0; i < n; i++) {
      for (let j = 0; j < n; j++) {
        if (i === j) continue;
        const li = system.nodes[i].local;
        const lj = system.nodes[j].local;
        if (lj.lt(li)) continue;
        const deltaLocal = lj.sub(li);
        const lowServer = deltaLocal.div(onePlusHi);
        const highServer = deltaLocal.div(onePlusLo);
        // Tj - Ti <= highServer
        add(i, j, highServer);
        // Ti - Tj <= -lowServer
        add(j, i, lowServer.neg());
      }
    }

    const distFromZ = bellmanFord(n + 1, edges, z);
    if (distFromZ === null) {
      system.feasible = false;
      throw new ClockConstraintError(`device ${system.key}: inconsistent clock constraints`);
    }
    const reversed = edges.map((edge) => ({
      from: edge.to,
      to: edge.from,
      weight: edge.weight,
    }));
    const distToZ = bellmanFord(n + 1, reversed, z);
    if (distToZ === null) {
      system.feasible = false;
      throw new ClockConstraintError(`device ${system.key}: inconsistent clock constraints`);
    }

    system.feasible = true;
    system.max = [];
    system.min = [];
    for (let i = 0; i < n; i++) {
      const max = distFromZ[i];
      const min = distToZ[i].neg();
      if (max === null || min === null || max.lt(min)) {
        system.feasible = false;
        throw new ClockConstraintError(`device ${system.key}: inconsistent clock bounds`);
      }
      system.max.push(max);
      system.min.push(min);
    }
  }

  localBounds(device: DeviceIdentity, local: RationalInput): { min: Rational; max: Rational } {
    const system = this.getSystem(device);
    if (!system.feasible) {
      throw new ClockConstraintError(`device ${system.key}: clock system has not been prepared`);
    }
    const value = rat(local);
    const key = localKey(value);
    for (let i = 0; i < system.nodes.length; i++) {
      if (localKey(system.nodes[i].local) === key) {
        return { min: system.min[i], max: system.max[i] };
      }
    }
    throw new ClockConstraintError(`device ${system.key}: local event ${value} is not registered`);
  }

  witness(device: DeviceIdentity): ClockWitness {
    const system = this.getSystem(device);
    if (!system.feasible) {
      throw new ClockConstraintError(`device ${system.key}: clock system has not been prepared`);
    }
    // distFromZ assigns every node its latest feasible server time.  This is a
    // single coherent assignment for the whole device, not a per-lease mixture.
    return {
      deviceKey: system.key,
      deviceId: system.device.id,
      generation: String(system.device.generation),
      offset: { lo: system.offset.lo, hi: system.offset.hi },
      drift: { lo: system.drift.lo, hi: system.drift.hi },
      events: system.nodes.map((node, i) => ({
        local: node.local,
        server: system.max[i],
        source: node.source,
      })),
    };
  }
}

function bellmanFord(nodeCount: number, edges: Edge[], source: number): Rational[] | null {
  const dist: Array<Rational | null> = Array.from({ length: nodeCount }, () => null);
  dist[source] = ZERO;
  for (let iteration = 0; iteration < nodeCount - 1; iteration++) {
    let changed = false;
    for (const edge of edges) {
      const from = dist[edge.from];
      if (from === null) continue;
      const candidate = from.add(edge.weight);
      const to = dist[edge.to];
      if (to === null || candidate.lt(to)) {
        dist[edge.to] = candidate;
        changed = true;
      }
    }
    if (!changed) break;
  }
  for (const edge of edges) {
    const from = dist[edge.from];
    if (from === null) continue;
    const candidate = from.add(edge.weight);
    const to = dist[edge.to];
    if (to === null || candidate.lt(to)) return null;
  }
  return dist.map((value) => value ?? ZERO);
}

export function clockWitnessToJSON(witness: ClockWitness): Record<string, unknown> {
  return {
    deviceKey: witness.deviceKey,
    deviceId: witness.deviceId,
    generation: witness.generation,
    offset: {
      lo: witness.offset.lo.toString(),
      hi: witness.offset.hi.toString(),
    },
    drift: {
      lo: witness.drift.lo.toString(),
      hi: witness.drift.hi.toString(),
    },
    events: witness.events.map((event) => ({
      local: event.local.toString(),
      server: event.server.toString(),
      source: event.source,
    })),
  };
}
