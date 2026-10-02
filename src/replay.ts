/**
 * 时间轴回放器：把审计流水渲染为人类可读的争抢全过程，
 * 并可输出任意事件的因果证据链。
 */
import type { AuditTrail } from './audit-trail.ts';
import type { AuditEvent } from './events.ts';
import { formatLogical, type LogicalTimestamp } from './logical-clock.ts';

function describe(event: AuditEvent): string {
  const d = event.details as Record<string, unknown>;
  switch (event.kind) {
    case 'LEASE_REQUESTED':
      return `节点 ${event.actorId} 申请租约（请求方时钟 ${formatLogical(d.requesterClock as LogicalTimestamp)}，ttl=${d.ttlMs}ms）`;
    case 'LEASE_GRANTED':
      return `租约授予 ${d.holderId}（fencingToken=${d.fencingToken}，到期@${d.expiresAtMs}）`;
    case 'LEASE_RENEWED':
      return `${event.actorId} 续期成功（fencingToken=${d.fencingToken}，新到期@${d.expiresAtMs}）`;
    case 'LEASE_TRANSFERRED':
      return `租约由 ${d.fromNodeId} 移交给 ${d.toNodeId}（fencingToken=${d.fencingToken}）`;
    case 'LEASE_EXPIRED':
      return `租约超时失效（原持有者 ${d.holderId}，fencingToken=${d.fencingToken}）`;
    case 'LEASE_REJECTED':
      return `节点 ${event.actorId} 被拒绝（原因=${d.reason}，当前持有者=${d.currentHolderId ?? '无'}）`;
    case 'CONTENTION_DETECTED':
      return `检测到竞态冲突：${d.loserNodeId}(req#${d.loserRequestSeq}) 争抢 ${d.winnerNodeId}(req#${d.winnerRequestSeq}) 持有的租约，仲裁器锁=${d.arbiterLockState}，同一毫秒到达=${d.sameMillisecond}`;
    case 'ARBITER_LOCK_ACQUIRED':
      return `仲裁器锁 ${d.from}→${d.to}（操作=${d.operation}）`;
    case 'ARBITER_LOCK_RELEASED':
      return `仲裁器锁 ${d.from}→${d.to}（操作=${d.operation}）`;
  }
}

export function formatEvent(event: AuditEvent): string {
  const causes = event.causedBy.length > 0 ? ` 因果<=${event.causedBy.map((s) => `#${s}`).join(',')}` : '';
  return `#${String(event.seq).padStart(2, '0')} [${event.kind}] ${formatLogical(event.clock)} wall=${event.wallTimeMs}ms ${describe(event)}${causes}`;
}

/** 按逻辑时间轴回放指定租约（或全部）的争抢全过程 */
export function renderTimeline(trail: AuditTrail, leaseId?: string): string {
  const lines = trail.timeline(leaseId).map(formatEvent);
  return [`=== 租约争抢时间轴回放${leaseId ? `（lease=${leaseId}）` : ''} ===`, ...lines].join('\n');
}

/** 输出某事件的因果证据链（根因 → … → 该事件） */
export function renderCausalChain(trail: AuditTrail, seq: number): string {
  const chain = trail.causalChain(seq);
  const lines = chain.map((event, i) => `${'  '.repeat(i)}${i === 0 ? '根因' : '导致'} ${formatEvent(event)}`);
  return [`=== 事件 #${seq} 的因果证据链 ===`, ...lines].join('\n');
}
