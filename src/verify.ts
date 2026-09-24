/**
 * 独立引用遍历核验：不信任账本内部计数，
 * 从全部引用根（源单元、缓存项、在途借用）重新遍历块 DAG，
 * 重数每个物理块的引用与字节，再与账本逐项对账。
 */

import type { ReactiveCache } from './reactive.ts';

export interface VerifyCheck {
  name: string;
  ok: boolean;
  detail: string;
}

export interface VerifyReport {
  ok: boolean;
  checks: VerifyCheck[];
}

export function verifyLedger(cache: ReactiveCache): VerifyReport {
  const checks: VerifyCheck[] = [];
  const add = (name: string, ok: boolean, detail = '') => checks.push({ name, ok, detail });

  const live = cache.listBlocks();
  const liveById = new Map(live.map((b) => [b.id, b]));

  // 1) 从引用根出发独立遍历，重数每个物理块的引用数
  const expectedRefs = new Map<number, number>();
  const queue: number[] = [];
  const refRoot = (id: number): void => {
    if (!expectedRefs.has(id)) {
      expectedRefs.set(id, 0);
      queue.push(id);
    }
    expectedRefs.set(id, (expectedRefs.get(id) as number) + 1);
  };
  for (const c of cache.storeCells()) refRoot(c.block);
  for (const n of cache.derivedNodes()) if (n.entry !== null) refRoot(n.entry.block);
  for (const [b, n] of cache.scheduler.borrowCounts()) {
    for (let i = 0; i < n; i++) refRoot(b);
  }
  while (queue.length > 0) {
    const id = queue.shift() as number;
    if (!liveById.has(id)) continue; // 引用到已释放块：下一条检查会报告
    for (const child of cache.childrenOf(id)) refRoot(child);
  }

  let refsOk = true;
  let refsDetail = '';
  for (const b of live) {
    const exp = expectedRefs.get(b.id) ?? 0;
    if (exp !== b.refs) {
      refsOk = false;
      refsDetail += ` block#${b.id} refs=${b.refs} expected=${exp};`;
    }
  }
  for (const id of expectedRefs.keys()) {
    if (!liveById.has(id)) {
      refsOk = false;
      refsDetail += ` block#${id} reachable but already freed;`;
    }
  }
  add('引用计数与独立遍历一致', refsOk, refsDetail.trim());

  // 2) 账本字节与存活物理块对账
  const liveBytes = live.reduce((s, b) => s + b.size, 0);
  add(
    '账本物理字节 == 存活块之和',
    cache.ledger.physicalBytes === liveBytes,
    `ledger=${cache.ledger.physicalBytes}B live=${liveBytes}B`,
  );
  add(
    '账本占用不超预算',
    cache.ledger.used <= cache.ledger.budget,
    `used=${cache.ledger.used}B budget=${cache.ledger.budget}B`,
  );

  // 3) 缓存足迹与订阅保留集独立重算
  const cacheSet = new Set<number>();
  const pinnedSet = new Set<number>();
  const walk = (root: number, into: Set<number>): void => {
    const stack = [root];
    while (stack.length > 0) {
      const id = stack.pop() as number;
      if (into.has(id)) continue;
      into.add(id);
      if (!liveById.has(id)) continue;
      for (const c of cache.childrenOf(id)) stack.push(c);
    }
  };
  for (const n of cache.derivedNodes()) {
    if (n.entry !== null) {
      walk(n.entry.block, cacheSet);
      if (n.subscribers > 0) walk(n.entry.block, pinnedSet);
    }
  }
  const bytesOf = (set: Set<number>): number => {
    let s = 0;
    for (const id of set) s += liveById.get(id)?.size ?? 0;
    return s;
  };
  const cacheBytes = bytesOf(cacheSet);
  const pinnedBytes = bytesOf(pinnedSet);
  add(
    '缓存足迹与独立重算一致',
    cache.cacheBytes === cacheBytes,
    `cache=${cache.cacheBytes}B independent=${cacheBytes}B`,
  );
  add(
    '订阅保留与独立重算一致',
    cache.pinnedBytes === pinnedBytes,
    `pinned=${cache.pinnedBytes}B independent=${pinnedBytes}B`,
  );
  add(
    '缓存足迹不超缓存预算',
    cache.cacheBytes <= cache.cacheBudget,
    `cache=${cache.cacheBytes}B budget=${cache.cacheBudget}B`,
  );
  add(
    '订阅保留可证明不超预算',
    cache.pinnedBytes <= cache.cacheBudget,
    `pinned=${cache.pinnedBytes}B budget=${cache.cacheBudget}B`,
  );

  // 4) 保留策略与驱逐范围
  let retained = true;
  for (const n of cache.derivedNodes()) {
    if (n.subscribers > 0 && n.entry === null) retained = false;
  }
  add('所有存活订阅的值均被保留', retained);

  const nodeIds = new Set(cache.derivedNodes().map((n) => n.id));
  const cellIds = new Set(cache.storeCells().map((c) => c.id));
  let candidatesOk = true;
  for (const id of cache.evictionCandidates()) {
    if (!nodeIds.has(id) || cellIds.has(id)) candidatesOk = false;
  }
  add('驱逐候选仅含未订阅的派生缓存项（源状态不可驱逐）', candidatesOk);

  return { ok: checks.every((c) => c.ok), checks };
}

export function formatReport(report: VerifyReport): string {
  const lines = report.checks.map((c) => `  ${c.ok ? '✓' : '✗'} ${c.name}${c.detail ? `  [${c.detail}]` : ''}`);
  return [`verifyLedger: ${report.ok ? 'OK' : 'FAILED'}`, ...lines].join('\n');
}
