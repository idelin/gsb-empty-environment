import { EARTH_RADIUS_M, normDeg } from './geo.ts';
import type { TrajectoryPoint } from './types.ts';

type Segment =
  | { kind: 'straight'; seconds: number; accel?: number; eleRate?: number }
  | { kind: 'turn'; seconds: number; yawRateDeg: number }
  | { kind: 'lane-change'; seconds: number; yawRateDeg: number }
  | { kind: 'arc'; seconds: number; radiusM: number };

/**
 * Deterministic synthetic drive-cycle generator (1 Hz sampling):
 * highway straights with acceleration, urban right-angle turns, a tight
 * roundabout arc, a lane change and an elevated ramp.
 */
export function generateDriveCycle(seed = 42): TrajectoryPoint[] {
  // Simple deterministic PRNG (mulberry32) for sensor noise.
  let state = seed >>> 0;
  const rand = (): number => {
    state = (state + 0x6d2b79f5) >>> 0;
    let z = state;
    z = Math.imul(z ^ (z >>> 15), z | 1);
    z ^= z + Math.imul(z ^ (z >>> 7), z | 61);
    return ((z ^ (z >>> 14)) >>> 0) / 4294967296;
  };
  const noise = (amp: number): number => (rand() - 0.5) * 2 * amp;

  const points: TrajectoryPoint[] = [];
  let t = 1_700_000_000_000;
  let lat = 31.2304;
  let lon = 121.4737;
  let ele = 8;
  let speed = 0;
  let heading = 90;

  const step = (dtMs: number): void => {
    const dtSec = dtMs / 1000;
    const dist = speed * dtSec;
    const rad = (heading * Math.PI) / 180;
    lat += (dist * Math.cos(rad)) / EARTH_RADIUS_M * (180 / Math.PI);
    lon +=
      (dist * Math.sin(rad)) /
      (EARTH_RADIUS_M * Math.cos((lat * Math.PI) / 180)) *
      (180 / Math.PI);
    t += dtMs;
    points.push({
      t,
      lat: lat + noise(2e-7),
      lon: lon + noise(2e-7),
      ele: ele + noise(0.15),
      speed: Math.max(0, speed + noise(0.05)),
      heading: normDeg(heading + noise(0.4)),
    });
  };

  const run = (seg: Segment): void => {
    const steps = Math.round(seg.seconds);
    for (let i = 0; i < steps; i++) {
      switch (seg.kind) {
        case 'straight':
          speed = Math.max(0, speed + (seg.accel ?? 0));
          ele += seg.eleRate ?? 0;
          break;
        case 'turn':
          heading = normDeg(heading + seg.yawRateDeg);
          break;
        case 'lane-change': {
          // Smooth S-shaped heading oscillation: out and back.
          const phase = Math.sin((i / steps) * Math.PI);
          heading = normDeg(heading + seg.yawRateDeg * phase);
          break;
        }
        case 'arc': {
          if (speed > 0.1) {
            const yaw = (speed / seg.radiusM) * (180 / Math.PI);
            heading = normDeg(heading + yaw);
          }
          break;
        }
      }
      step(1000);
    }
  };

  const plan: Segment[] = [
    { kind: 'straight', seconds: 20, accel: 1.2 }, // 0 -> 24 m/s, straight acceleration
    { kind: 'straight', seconds: 240 }, // highway cruise (long straight)
    { kind: 'lane-change', seconds: 4, yawRateDeg: 6 }, // overtake
    { kind: 'straight', seconds: 60 },
    { kind: 'lane-change', seconds: 4, yawRateDeg: -6 }, // back to lane
    { kind: 'straight', seconds: 40, accel: -0.45 }, // slow down for exit
    { kind: 'turn', seconds: 6, yawRateDeg: 15 }, // 90 deg right turn
    { kind: 'straight', seconds: 30, accel: 0.3 },
    { kind: 'arc', seconds: 25, radiusM: 45 }, // roundabout, tight radius
    { kind: 'straight', seconds: 25 },
    { kind: 'turn', seconds: 9, yawRateDeg: -10 }, // 90 deg left turn
    { kind: 'straight', seconds: 35, eleRate: 0.35 }, // climb onto overpass
    { kind: 'straight', seconds: 20, eleRate: -0.35 }, // descend
    { kind: 'straight', seconds: 45, accel: -0.3 }, // roll to a stop
  ];
  for (const seg of plan) run(seg);

  return points;
}
