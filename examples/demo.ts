/**
 * 运行样例：无人机编队飞行散点的动态空域安全边界演化。
 *
 * 两个无人机编队沿各自航线飞行，每架无人机每个时隙上报一次位置，
 * 每条上报带有 TTL（超时未刷新则视为老探测点自然失效淡出）。
 * 演化器逐时隙增量维护存活点集的凸包（动态安全边界），
 * 全程不做任何一次全量 Graham 扫描。
 *
 * 运行：npm run demo
 */
import { DynamicConvexHull } from "../src/dynamic-hull.ts";
import { signedArea, type Vec } from "../src/geometry.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rng = mulberry32(20261002);
const SLOTS = 24;
const TTL = 6; // 位置上报 6 个时隙后失效
const DRONES_PER_FLEET = 7;

interface Drone {
  fleet: number;
  phase: number;
  radius: number;
}

const drones: Drone[] = [];
for (let f = 0; f < 2; f++) {
  for (let i = 0; i < DRONES_PER_FLEET; i++) {
    drones.push({ fleet: f, phase: (i / DRONES_PER_FLEET) * Math.PI * 2, radius: 9 + rng() * 3 });
  }
}

/** 第 t 时隙第 f 编队的中心位置（编队 1 在 t=12 后返航离场） */
function fleetCenter(fleet: number, t: number): Vec | null {
  if (fleet === 0) return { x: 20 + t * 1.5, y: 30 + Math.sin(t / 4) * 6 };
  if (t >= 12) return null; // 编队离场，上报停止，旧点将随 TTL 淡出
  return { x: 90 - t * 2.0, y: 25 + Math.cos(t / 3) * 5 };
}

const evolver = new DynamicConvexHull();
let fullScanCost = 0; // 对照组：每时隙全量 Graham 扫描的代价上界 n·log2(n)

console.log("=== 无人机编队动态空域安全边界（凸包）演化 ===");
console.log(`参数: ${drones.length} 架无人机 / TTL=${TTL} 时隙 / 共 ${SLOTS} 个时隙\n`);

for (let t = 0; t < SLOTS; t++) {
  for (const d of drones) {
    const c = fleetCenter(d.fleet, t);
    if (!c) continue;
    const angle = d.phase + t * 0.35;
    const x = c.x + Math.cos(angle) * d.radius + (rng() - 0.5) * 1.5;
    const y = c.y + Math.sin(angle) * d.radius + (rng() - 0.5) * 1.5;
    evolver.schedule(x, y, TTL, t);
  }

  const hull = evolver.advanceTo(t);
  const alive = evolver.aliveCount;
  fullScanCost += alive > 1 ? Math.ceil(alive * Math.log2(alive)) : alive;

  const verts = hull.map((p) => `(${p.x.toFixed(1)},${p.y.toFixed(1)})`).join(" ");
  const area = hull.length >= 3 ? signedArea(hull).toFixed(1) : "0.0";
  console.log(
    `t=${String(t).padStart(2)}  存活=${String(alive).padStart(3)}  顶点=${hull.length}  面积=${area.padStart(8)}  边界: ${verts}`,
  );
}

const stats = evolver.getStats();
console.log("\n=== 增量维护统计 ===");
console.log(`累计进入点:        ${stats.insertedTotal}`);
console.log(`累计过期点:        ${stats.expiredTotal}`);
console.log(`插入触发链扫描:    ${stats.insertSweeps} 次（其余 ${stats.insertedTotal - stats.insertSweeps} 次插入落在包络内，O(log n) 忽略）`);
console.log(`过期触发局部修复:  ${stats.localRepairs} 次（其余 ${stats.expiredTotal - stats.localRepairs} 次过期点非顶点，零几何代价）`);
console.log(`局部修复扫描点数:  ${stats.repairPointsScanned}`);
console.log(`全量 Graham 重扫:  ${stats.fullRescans} 次（恒为 0，绝不全量重算）`);
console.log(`对照组全量扫描代价: ${fullScanCost}（每时隙 n·log n 重算的理论总量）`);
