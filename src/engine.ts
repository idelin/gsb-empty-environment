import type { Vec3 } from './vec3.ts';
import { extrapolate, distance } from './vec3.ts';
import type { Capsule } from './capsule.ts';
import { makeCapsule, capsuleDistance, timeOfSeparationBreach } from './capsule.ts';
import { SpacetimeGrid } from './spacetimeGrid.ts';

export interface Entity {
  id: string;
  position: Vec3;
  velocity: Vec3;
  /** Body radius in meters; capsule radius of the swept volume. */
  radius: number;
}

export interface EngineConfig {
  /** Grid cell edge length in meters. */
  cellSize: number;
  /** Time slot duration in seconds. */
  slotDuration: number;
  /** Prediction horizon in seconds (how far ahead capsules are extrapolated). */
  horizon: number;
  /** Minimum safe surface-to-surface separation in meters. */
  minSeparation: number;
  /** Beacons fire when a breach is predicted within this many seconds. */
  warningLeadTime: number;
}

export interface WarningBeacon {
  entityA: string;
  entityB: string;
  /** Absolute time the beacon was issued. */
  issuedAt: number;
  /** Predicted absolute time of the separation breach. */
  predictedBreachAt: number;
  /** Seconds from issuance until the predicted breach. */
  timeToBreach: number;
  /** Current surface-to-surface distance between the two entities. */
  currentDistance: number;
  minSeparation: number;
}

const pairKey = (a: string, b: string): string => (a < b ? `${a}|${b}` : `${b}|${a}`);

/**
 * Large-scale dynamic-entity proximity engine. Each scan extrapolates every
 * entity's swept capsule over the upcoming time slots, hashes them into a
 * spatiotemporal grid for constant-time broad-phase pairing, then runs exact
 * capsule/kinematic narrow-phase checks on candidate pairs only.
 */
export class CollisionWarningEngine {
  readonly config: EngineConfig;
  private entities: Entity[] = [];
  private indexOf = new Map<string, number>();
  private grid: SpacetimeGrid;
  private warned = new Map<string, WarningBeacon>();
  private now = 0;

  constructor(config: EngineConfig) {
    this.config = config;
    this.grid = new SpacetimeGrid(config.cellSize, config.slotDuration);
  }

  addEntity(entity: Entity): void {
    if (this.indexOf.has(entity.id)) {
      throw new Error(`duplicate entity id: ${entity.id}`);
    }
    this.indexOf.set(entity.id, this.entities.length);
    this.entities.push(entity);
  }

  getEntity(id: string): Entity | undefined {
    const i = this.indexOf.get(id);
    return i === undefined ? undefined : this.entities[i];
  }

  get entityCount(): number {
    return this.entities.length;
  }

  get currentTime(): number {
    return this.now;
  }

  /** Integrate all entities forward by dt seconds. */
  advance(dt: number): void {
    this.now += dt;
    for (const e of this.entities) {
      e.position = extrapolate(e.position, e.velocity, dt);
    }
  }

  /** Swept capsule of an entity over the absolute time window [t0, t1]. */
  private capsuleFor(e: Entity, t0: number, t1: number): Capsule {
    return makeCapsule(
      extrapolate(e.position, e.velocity, t0 - this.now),
      extrapolate(e.position, e.velocity, t1 - this.now),
      e.radius,
    );
  }

  /**
   * Broad + narrow phase over the prediction horizon. Returns beacons for
   * pairs newly predicted to breach `minSeparation` within `warningLeadTime`.
   */
  scan(): WarningBeacon[] {
    const { slotDuration, horizon, minSeparation, warningLeadTime } = this.config;
    const entities = this.entities;
    const checked = new Set<string>();
    const activePairs = new Set<string>();
    const beacons: WarningBeacon[] = [];

    const slotCount = Math.ceil(horizon / slotDuration);
    for (let s = 0; s < slotCount; s++) {
      const t0 = this.now + s * slotDuration;
      const t1 = t0 + slotDuration;
      const slot = this.grid.slotOf(t0);

      this.grid.clear();
      const capsules: Capsule[] = new Array(entities.length);
      for (let i = 0; i < entities.length; i++) {
        const capsule = this.capsuleFor(entities[i], t0, t1);
        capsules[i] = capsule;
        this.grid.insert(slot, i, capsule);
      }

      for (let i = 0; i < entities.length; i++) {
        for (const j of this.grid.queryNeighbors(slot, i, capsules[i])) {
          if (j <= i) continue; // each unordered pair once
          const a = entities[i];
          const b = entities[j];

          // Narrow phase 1: exact capsule clearance for this slot.
          if (capsuleDistance(capsules[i], capsules[j]) >= minSeparation) continue;

          const key = pairKey(a.id, b.id);
          if (checked.has(key)) continue;
          checked.add(key);

          // Narrow phase 2: analytic time of breach under constant velocity.
          const breachIn = timeOfSeparationBreach(
            a.position,
            a.velocity,
            b.position,
            b.velocity,
            minSeparation + a.radius + b.radius,
            0,
          );
          if (breachIn === undefined || breachIn > horizon) continue;

          activePairs.add(key);
          if (breachIn <= warningLeadTime && !this.warned.has(key)) {
            const beacon: WarningBeacon = {
              entityA: a.id,
              entityB: b.id,
              issuedAt: this.now,
              predictedBreachAt: this.now + breachIn,
              timeToBreach: breachIn,
              currentDistance: Math.max(
                0,
                distance(a.position, b.position) - a.radius - b.radius,
              ),
              minSeparation,
            };
            this.warned.set(key, beacon);
            beacons.push(beacon);
          }
        }
      }
    }

    // Retire beacons for pairs no longer on a breach course.
    for (const key of [...this.warned.keys()]) {
      if (!activePairs.has(key)) this.warned.delete(key);
    }

    return beacons;
  }

  /** All currently active beacons. */
  activeWarnings(): WarningBeacon[] {
    return [...this.warned.values()];
  }
}
