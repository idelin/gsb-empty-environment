/**
 * 验收场景：500 个刚体同时运动，其中恰好 12 对包围盒重叠。
 *
 * - 476 个刚体分布在 8x8x8 网格中（间距 10，盒半径 0.5，互不重叠）；
 * - 12 对刚体放置在独立区域，每对内部重叠、对间远离；
 * - 所有刚体每帧按各自速度运动（同对成员共享速度，保持重叠状态）。
 */
import type { AABB } from "../src/aabb.ts";
import { overlaps } from "../src/aabb.ts";

export const BODY_COUNT = 500;
export const EXPECTED_PAIR_COUNT = 12;
export const HALF_EXTENT = 0.5;

export interface SceneBody {
  id: number;
  position: [number, number, number];
  velocity: [number, number, number];
}

export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function bodyAabb(body: SceneBody): AABB {
  const [x, y, z] = body.position;
  return {
    minX: x - HALF_EXTENT, minY: y - HALF_EXTENT, minZ: z - HALF_EXTENT,
    maxX: x + HALF_EXTENT, maxY: y + HALF_EXTENT, maxZ: z + HALF_EXTENT,
  };
}

export function buildScene(): SceneBody[] {
  const rand = mulberry32(20261002);
  const bodies: SceneBody[] = [];
  const randomVelocity = (): [number, number, number] => [
    (rand() * 2 - 1) * 0.02,
    (rand() * 2 - 1) * 0.02,
    (rand() * 2 - 1) * 0.02,
  ];

  // 476 个刚体：8x8x8 网格（取前 476 个），间距 10，互不重叠。
  const spacing = 10;
  let id = 0;
  outer: for (let gx = 0; gx < 8; gx++) {
    for (let gy = 0; gy < 8; gy++) {
      for (let gz = 0; gz < 8; gz++) {
        if (id >= BODY_COUNT - EXPECTED_PAIR_COUNT * 2) break outer;
        bodies.push({
          id,
          position: [gx * spacing, gy * spacing, gz * spacing],
          velocity: randomVelocity(),
        });
        id++;
      }
    }
  }

  // 12 对重叠刚体：放在远离网格的区域，对间间隔 10，对内中心距 0.5（重叠）。
  const pairBaseY = 500;
  for (let p = 0; p < EXPECTED_PAIR_COUNT; p++) {
    const velocity = randomVelocity();
    const px = p * spacing;
    bodies.push({ id: id++, position: [px, pairBaseY, 0], velocity: [...velocity] });
    bodies.push({ id: id++, position: [px + 0.5, pairBaseY, 0], velocity: [...velocity] });
  }

  return bodies;
}

export function stepScene(bodies: SceneBody[]): void {
  for (const body of bodies) {
    body.position[0] += body.velocity[0];
    body.position[1] += body.velocity[1];
    body.position[2] += body.velocity[2];
  }
}

/** O(N^2) 暴力比对（基准对照）。 */
export function bruteForcePairs(bodies: SceneBody[]): Array<[number, number]> {
  const aabbs = bodies.map(bodyAabb);
  const pairs: Array<[number, number]> = [];
  for (let i = 0; i < bodies.length; i++) {
    for (let j = i + 1; j < bodies.length; j++) {
      if (overlaps(aabbs[i], aabbs[j])) {
        pairs.push([bodies[i].id, bodies[j].id]);
      }
    }
  }
  return pairs;
}
