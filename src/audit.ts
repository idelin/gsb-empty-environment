// 独立审计器：只读持久化账本，重建事实并验证协议不变式。
// - 中心：逐条重放，任一时刻 grossIssued(w) - confirmedReturned(w) <= budget；
//   授予 expiry 不得跨窗；归还金额合法；重复归还不重复计入。
// - 节点：接受的请求按 requestId 全局去重（同 ID 只计一次）；
//   分摊必须来自已收到、未归还、未过期的授予且不超过其余额。
// - 汇总：每窗 consumed <= budget，给出 idle = budget - consumed 与拒绝原因。
import fs from 'node:fs';
import path from 'node:path';

export interface WindowAudit {
  windowId: number;
  budget: number;
  consumed: number;
  idle: number;
  ok: boolean;
}

export interface AuditReport {
  ok: boolean;
  windows: WindowAudit[];
  acceptedUnique: number;
  duplicateRequestIds: string[];
  rejections: Record<string, number>;
  issueRejections: Record<string, number>;
  conflicts: number;
  violations: string[];
  unconfirmedReturns: Array<{ nodeId: string; grantId: string; unspent: number }>;
  lostGrants: Array<{ nodeId: string; grantId: string; amount: number }>;
}

function readJsonl(file: string): any[] {
  if (!fs.existsSync(file)) return [];
  return fs
    .readFileSync(file, 'utf8')
    .split('\n')
    .filter((l) => l.trim().length > 0)
    .map((l) => JSON.parse(l));
}

export function auditDir(dataDir: string, windowLength: number, budget: number): AuditReport {
  const violations: string[] = [];
  const windowEnd = (w: number): number => (w + 1) * windowLength - 1;

  // ---- 中心账本重放 ----
  const centerRecs = readJsonl(path.join(dataDir, 'center.jsonl'));
  const gross = new Map<number, number>();
  const returned = new Map<number, number>();
  const grants = new Map<string, { windowId: number; nodeId: string; amount: number; returned: boolean }>();
  const issueRejections: Record<string, number> = {};
  for (const rec of centerRecs) {
    if (rec.t === 'issue') {
      const g = rec.grant;
      if (g.expiresAtTick > windowEnd(g.windowId)) {
        violations.push(`grant ${g.grantId} expiry ${g.expiresAtTick} crosses window ${g.windowId}`);
      }
      if (Math.floor(g.issuedTick / windowLength) !== g.windowId) {
        violations.push(`grant ${g.grantId} issuedTick ${g.issuedTick} not in window ${g.windowId}`);
      }
      const w = g.windowId;
      gross.set(w, (gross.get(w) ?? 0) + g.amount);
      grants.set(g.grantId, { windowId: w, nodeId: g.nodeId, amount: g.amount, returned: false });
      const net = (gross.get(w) ?? 0) - (returned.get(w) ?? 0);
      if (net > budget) violations.push(`window ${w}: net issued ${net} exceeds budget ${budget}`);
    } else if (rec.t === 'issue-rejected') {
      issueRejections[rec.reason] = (issueRejections[rec.reason] ?? 0) + 1;
    } else if (rec.t === 'return') {
      const book = grants.get(rec.grantId);
      if (!book) {
        violations.push(`return ${rec.returnId} references unknown grant ${rec.grantId}`);
      } else if (book.returned) {
        violations.push(`return ${rec.returnId} double-counted for grant ${rec.grantId}`);
      } else if (rec.unspent < 0 || rec.unspent > book.amount) {
        violations.push(`return ${rec.returnId} invalid unspent ${rec.unspent} for grant ${rec.grantId}`);
      } else {
        book.returned = true;
        returned.set(book.windowId, (returned.get(book.windowId) ?? 0) + rec.unspent);
      }
    } else if (rec.t === 'return-dup') {
      const book = grants.get(rec.grantId);
      if (!book || !book.returned) violations.push(`return-dup ${rec.returnId} without prior return for ${rec.grantId}`);
    }
  }

  // ---- 节点账本重放 ----
  const nodeFiles = fs
    .readdirSync(dataDir)
    .filter((f) => /^node-.+\.jsonl$/.test(f))
    .sort();
  const accepted = new Map<string, { nodeId: string; cost: number; windowId: number; tick: number }>();
  const duplicateRequestIds: string[] = [];
  const rejections: Record<string, number> = {};
  const consumed = new Map<number, number>();
  const unconfirmedReturns: Array<{ nodeId: string; grantId: string; unspent: number }> = [];
  const receivedGrantIds = new Set<string>();
  let conflicts = 0;

  for (const file of nodeFiles) {
    const nodeId = file.replace(/^node-/, '').replace(/\.jsonl$/, '');
    const recs = readJsonl(path.join(dataDir, file));
    const local = new Map<string, { amount: number; expiresAtTick: number; spent: number; returned: boolean }>();
    const localReturns = new Map<string, { grantId: string; unspent: number }>();
    for (const rec of recs) {
      if (rec.t === 'grant') {
        local.set(rec.grant.grantId, { amount: rec.grant.amount, expiresAtTick: rec.grant.expiresAtTick, spent: 0, returned: false });
        receivedGrantIds.add(rec.grant.grantId);
      } else if (rec.t === 'spend-accepted') {
        if (Math.floor(rec.tick / windowLength) !== rec.windowId) {
          violations.push(`${nodeId} req=${rec.requestId}: windowId ${rec.windowId} != windowOf(${rec.tick})`);
        }
        const allocSum = rec.allocs.reduce((s: number, a: any) => s + a.amount, 0);
        if (allocSum !== rec.cost) violations.push(`${nodeId} req=${rec.requestId}: allocs sum ${allocSum} != cost ${rec.cost}`);
        for (const a of rec.allocs) {
          const g = local.get(a.grantId);
          if (!g) {
            violations.push(`${nodeId} req=${rec.requestId}: alloc from unreceived grant ${a.grantId}`);
            continue;
          }
          if (g.returned) violations.push(`${nodeId} req=${rec.requestId}: alloc from returned grant ${a.grantId}`);
          if (g.expiresAtTick < rec.tick) violations.push(`${nodeId} req=${rec.requestId}: alloc from expired grant ${a.grantId}`);
          g.spent += a.amount;
          if (g.spent > g.amount) violations.push(`${nodeId} req=${rec.requestId}: grant ${a.grantId} overspent (${g.spent}>${g.amount})`);
        }
        if (accepted.has(rec.requestId)) {
          duplicateRequestIds.push(rec.requestId);
        } else {
          accepted.set(rec.requestId, { nodeId, cost: rec.cost, windowId: rec.windowId, tick: rec.tick });
          consumed.set(rec.windowId, (consumed.get(rec.windowId) ?? 0) + rec.cost);
        }
      } else if (rec.t === 'spend-rejected') {
        rejections[rec.reason] = (rejections[rec.reason] ?? 0) + 1;
      } else if (rec.t === 'spend-conflict') {
        conflicts += 1;
      } else if (rec.t === 'return') {
        const g = local.get(rec.grantId);
        if (g) g.returned = true;
        localReturns.set(rec.returnId, { grantId: rec.grantId, unspent: rec.unspent });
      } else if (rec.t === 'return-confirm') {
        localReturns.delete(rec.returnId);
      }
    }
    for (const [returnId, r] of localReturns) {
      unconfirmedReturns.push({ nodeId, grantId: r.grantId, unspent: r.unspent });
      void returnId;
    }
  }

  // 中心签发但节点从未收到的授予（丢失/分区/迟到前）。
  const lostGrants: Array<{ nodeId: string; grantId: string; amount: number }> = [];
  for (const rec of centerRecs) {
    if (rec.t === 'issue' && !receivedGrantIds.has(rec.grant.grantId)) {
      lostGrants.push({ nodeId: rec.grant.nodeId, grantId: rec.grant.grantId, amount: rec.grant.amount });
    }
  }

  // ---- 窗口汇总 ----
  const windowIds = new Set<number>([...gross.keys(), ...consumed.keys()]);
  const windows: WindowAudit[] = [...windowIds].sort((a, b) => a - b).map((w) => {
    const c = consumed.get(w) ?? 0;
    const ok = c <= budget;
    if (!ok) violations.push(`window ${w}: consumed ${c} exceeds budget ${budget}`);
    return { windowId: w, budget, consumed: c, idle: budget - c, ok };
  });

  const ok = violations.length === 0 && duplicateRequestIds.length === 0;
  return {
    ok,
    windows,
    acceptedUnique: accepted.size,
    duplicateRequestIds,
    rejections,
    issueRejections,
    conflicts,
    violations,
    unconfirmedReturns,
    lostGrants,
  };
}
