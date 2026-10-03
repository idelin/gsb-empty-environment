import { createHash } from 'node:crypto';
import { LsmEngine } from './engine.ts';
import { LsmDiagnosticsMonitor } from './monitor.ts';

/** 确定性伪随机数生成器（mulberry32），保证演示与测试可复现。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randomHashKey(rng: () => number): string {
  return createHash('sha1').update(`${rng()}`).digest('hex');
}

export interface Scenario {
  engine: LsmEngine;
  monitor: LsmDiagnosticsMonitor;
  allKeys: string[];
}

/** 各批次写入的键数：前期大批量导入，后续增量批次较小 → 共 9 张 L0 SSTable。 */
export const BATCH_SIZES = [1000, 900, 800, 700, 600, 500, 400, 300, 200];

export function buildScenario(seed = 20261004): Scenario {
  const rng = mulberry32(seed);
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 500, minSamples: 100, readAmpWarn: 2.0 });
  const engine = new LsmEngine({ bitsPerKey: 10, monitor });

  const seen = new Set<string>();
  for (const size of BATCH_SIZES) {
    let written = 0;
    while (written < size) {
      const key = randomHashKey(rng);
      if (seen.has(key)) continue;
      seen.add(key);
      engine.put(key, `v-${key.slice(0, 8)}`);
      written++;
    }
    engine.flush();
  }

  return { engine, monitor, allKeys: [...seen] };
}

/** 从已写入键集中均匀抽样 count 个查询键。 */
export function sampleKeys(allKeys: string[], count: number, rng: () => number): string[] {
  const out: string[] = new Array(count);
  for (let i = 0; i < count; i++) {
    out[i] = allKeys[Math.floor(rng() * allKeys.length)];
  }
  return out;
}
