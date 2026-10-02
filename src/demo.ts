import { deriveKeys, generateMasterKey } from "./crypto.ts";
import { TrajectoryIndex } from "./store.ts";
import { AuditAuthority, AuditLog, Auditor } from "./audit.ts";

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const T0 = Date.UTC(2026, 8, 30, 0, 0, 0);

function seededRandom(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 0xffffffff;
  };
}

const masterKey = generateMasterKey();
const keys = deriveKeys(masterKey);
const index = new TrajectoryIndex(keys, {
  bucketSizeMs: HOUR,
  finePrecision: 3,
  coarsePrecision: 1,
  kAnonymity: 5,
});

const rand = seededRandom(42);
const crowdCells = [
  { lat: 31.23, lon: 121.47 },
  { lat: 31.22, lon: 121.48 },
  { lat: 30.67, lon: 104.06 },
];
for (let user = 0; user < 300; user++) {
  const home = crowdCells[Math.floor(rand() * crowdCells.length)];
  for (let h = 0; h < 48; h++) {
    if (rand() < 0.4) continue;
    index.ingest({
      entityId: `user-${1000 + user}`,
      lat: home.lat + (rand() - 0.5) * 0.08,
      lon: home.lon + (rand() - 0.5) * 0.08,
      timestamp: T0 + h * HOUR + Math.floor(rand() * HOUR),
    });
  }
}

const target = "user-8842";
const targetTrail = [
  { lat: 31.2304, lon: 121.4737, h: 9 },
  { lat: 31.2343, lon: 121.4801, h: 10 },
  { lat: 31.2089, lon: 121.4355, h: 12 },
  { lat: 31.1943, lon: 121.4012, h: 15 },
  { lat: 31.2304, lon: 121.4737, h: 33 },
];
for (const p of targetTrail) {
  index.ingest({
    entityId: target,
    lat: p.lat,
    lon: p.lon,
    timestamp: T0 + p.h * HOUR + 7 * 60 * 1000,
  });
}

console.log("=== 1. 海量位置写入 ===");
console.log(`已写入加密轨迹记录: ${index.size} 条`);

console.log("\n=== 2. 时变假名（同一实体，逐小时轮换） ===");
for (const h of [9, 10, 12]) {
  console.log(
    `  ${target} @ hour ${String(h).padStart(2, "0")} -> ${index.inspectPseudonymOf(target, T0 + h * HOUR)}`,
  );
}

console.log("\n=== 3. 日常只读查询：脱敏聚合热力分布（k=5 匿名阈值） ===");
const heat = index.heatmap(T0, T0 + 2 * DAY);
for (const cell of heat) {
  console.log(`  粗网格 ${cell.cellId}  热度 ${cell.count}`);
}
console.log("  （输出仅含粗网格与计数，不含任何假名、精确坐标或真实 ID）");

console.log("\n=== 4. 无授权 / 伪造凭证的还原尝试 ===");
const authority = AuditAuthority.generate("judicial-authority-cn-east");
const log = new AuditLog();
const auditor = new Auditor(authority, index, log);
const forged = authority.issue("CASE-2026-1017", target, T0, T0 + DAY, DAY);
forged.entityId = "user-6666";
try {
  auditor.reveal(forged);
} catch (err) {
  console.log(`  伪造凭证被拒绝: ${(err as Error).message}`);
}

console.log("\n=== 5. 出示司法审计授权凭证：定向还原目标轨迹 ===");
const credential = authority.issue(
  "CASE-2026-1017",
  target,
  T0 + 8 * HOUR,
  T0 + 16 * HOUR,
  2 * HOUR,
);
console.log(
  `  凭证: case=${credential.caseId} target=${credential.entityId} 窗口=[+8h, +16h]`,
);
const revealed = auditor.reveal(credential);
for (const p of revealed) {
  const iso = new Date(p.timestamp).toISOString();
  console.log(`  ${iso}  (${p.lat.toFixed(4)}, ${p.lon.toFixed(4)})`);
}

console.log("\n=== 6. 审计日志（哈希链，可验证） ===");
for (const entry of log.list()) {
  console.log(
    `  #${entry.seq} ${entry.action} case=${entry.caseId} target=${entry.entityId}` +
      ` 结果=${entry.resultCount} hash=${entry.hash.slice(0, 16)}…`,
  );
}
console.log(`  审计链完整性校验: ${log.verify() ? "通过" : "失败"}`);
