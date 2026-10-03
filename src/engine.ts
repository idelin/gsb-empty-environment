import {
  Capsule,
  Vec3,
  add,
  capsuleSurfaceGap,
  distance,
  firstContactTime,
  scale,
  sub,
} from './geometry';
import { SpatioTemporalGrid } from './grid';

/** A dynamic entity tracked by the engine. */
export interface TrackedEntity {
  id: string;
  position: Vec3;
  velocity: Vec3;
  radius: number;
}

/** Early-warning beacon emitted before a predicted separation breach. */
export interface WarningBeacon {
  entityA: string;
  entityB: string;
  /** Simulation time at which the beacon was issued. */
  issuedAt: number;
  /** Absolute simulation time at which the breach is predicted. */
  predictedBreachTime: number;
  /** Seconds between issuance and the predicted breach. */
  timeToBreach: number;
  /** Current center-to-center (topological) distance of the pair. */
  currentDistance: number;
  /** Current surface-to-surface gap of the pair. */
  surfaceGap: number;
  /** Configured minimum safe separation that will be breached. */
  minSeparation: number;
  /** Time slot in which the breach is predicted. */
  slot: number;
}

export interface EngineOptions {
  /** Minimum safe surface-to-surface separation in meters. */
  minSeparation: number;
  /** Duration of one time slot in seconds (default 1). */
  slotDuration?: number;
  /** Number of future slots scanned per evaluation (default 10). */
  horizonSlots?: number;
  /** Spatial cell edge length in meters (default 256). */
  cellSize?: number;
}

export interface EngineStats {
  cellsUsed: number;
  candidatePairs: number;
  preciseChecks: number;
}

/**
 * Large-scale dynamic-entity proximity contact detection engine.
 *
 * Per evaluation tick every entity's trajectory for the next
 * `horizonSlots * slotDuration` seconds is extrapolated into swept capsules,
 * which are hashed into a spatio-temporal grid. Pairs sharing a
 * (slot, cell) bucket are candidate collision courses and are verified with
 * an exact capsule-capsule distance test plus a ballistic contact-time
 * solve. Insertion and lookup are O(1) hash operations, so the broad phase
 * scales linearly with the entity count instead of quadratically.
 */
export class CollisionWarningEngine {
  readonly minSeparation: number;
  readonly slotDuration: number;
  readonly horizonSlots: number;
  private readonly grid: SpatioTemporalGrid;
  private readonly alerts = new Map<string, WarningBeacon>();
  readonly stats: EngineStats = { cellsUsed: 0, candidatePairs: 0, preciseChecks: 0 };

  constructor(options: EngineOptions) {
    this.minSeparation = options.minSeparation;
    this.slotDuration = options.slotDuration ?? 1;
    this.horizonSlots = options.horizonSlots ?? 10;
    this.grid = new SpatioTemporalGrid(options.cellSize ?? 256);
  }

  /** Horizon in seconds covered by one evaluation. */
  get horizonSeconds(): number {
    return this.slotDuration * this.horizonSlots;
  }

  /**
   * Evaluate all entities at simulation time `now`.
   * Returns only newly raised beacons (each pair is alerted once).
   */
  evaluate(entities: TrackedEntity[], now: number): WarningBeacon[] {
    const dt = this.slotDuration;
    // Pad each capsule by half the minimum separation so that any pair whose
    // surface gap drops below minSeparation is guaranteed to share a cell.
    const padding = this.minSeparation / 2;

    this.grid.clear();
    this.stats.cellsUsed = 0;
    this.stats.candidatePairs = 0;
    this.stats.preciseChecks = 0;

    // Broad phase: extrapolate swept capsules and hash them into the grid.
    const capsules: Capsule[][] = new Array<Capsule[]>(entities.length);
    for (let i = 0; i < entities.length; i++) {
      const e = entities[i];
      const perSlot = new Array<Capsule>(this.horizonSlots + 1);
      for (let slot = 1; slot <= this.horizonSlots; slot++) {
        const capsule: Capsule = {
          start: add(e.position, scale(e.velocity, (slot - 1) * dt)),
          end: add(e.position, scale(e.velocity, slot * dt)),
          radius: e.radius,
        };
        perSlot[slot] = capsule;
        this.grid.insert(i, slot, {
          start: capsule.start,
          end: capsule.end,
          radius: capsule.radius + padding,
        });
      }
      capsules[i] = perSlot;
    }
    this.stats.cellsUsed = this.grid.cellCount;

    // Candidate pairs: entities sharing a spatio-temporal bucket.
    const earliestSlotByPair = new Map<string, number>();
    for (let i = 0; i < entities.length; i++) {
      for (let slot = 1; slot <= this.horizonSlots; slot++) {
        const padded: Capsule = {
          start: capsules[i][slot].start,
          end: capsules[i][slot].end,
          radius: capsules[i][slot].radius + padding,
        };
        const neighbors = this.grid.query(i, slot, padded);
        for (const j of neighbors) {
          if (j <= i) continue;
          const key = i + ':' + j;
          const known = earliestSlotByPair.get(key);
          if (known === undefined || slot < known) earliestSlotByPair.set(key, slot);
        }
      }
    }
    this.stats.candidatePairs = earliestSlotByPair.size;

    // Narrow phase: exact capsule distance + ballistic contact-time solve.
    const beacons: WarningBeacon[] = [];
    const ordered = [...earliestSlotByPair.entries()].sort((a, b) => a[1] - b[1]);
    for (const [key, slot] of ordered) {
      const sepIdx = key.indexOf(':');
      const i = Number(key.slice(0, sepIdx));
      const j = Number(key.slice(sepIdx + 1));
      const a = entities[i];
      const b = entities[j];
      this.stats.preciseChecks++;
      if (capsuleSurfaceGap(capsules[i][slot], capsules[j][slot]) >= this.minSeparation) {
        continue;
      }

      const contactThreshold = a.radius + b.radius + this.minSeparation;
      const slotStart = (slot - 1) * dt;
      const relPos = sub(
        add(a.position, scale(a.velocity, slotStart)),
        add(b.position, scale(b.velocity, slotStart)),
      );
      const relVel = sub(a.velocity, b.velocity);
      const tau = firstContactTime(relPos, relVel, contactThreshold, dt);
      const timeToBreach = slotStart + (tau === null ? 0 : tau);

      const idA = a.id < b.id ? a.id : b.id;
      const idB = a.id < b.id ? b.id : a.id;
      const pairKey = idA + '|' + idB;
      if (this.alerts.has(pairKey)) continue;

      const centerDistance = distance(a.position, b.position);
      const beacon: WarningBeacon = {
        entityA: idA,
        entityB: idB,
        issuedAt: now,
        predictedBreachTime: now + timeToBreach,
        timeToBreach,
        currentDistance: centerDistance,
        surfaceGap: centerDistance - a.radius - b.radius,
        minSeparation: this.minSeparation,
        slot,
      };
      this.alerts.set(pairKey, beacon);
      beacons.push(beacon);
    }
    return beacons;
  }

  getActiveAlerts(): WarningBeacon[] {
    return [...this.alerts.values()];
  }

  resetAlerts(): void {
    this.alerts.clear();
  }
}
