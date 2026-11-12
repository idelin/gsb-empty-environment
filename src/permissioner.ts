import type {
  ClipboardPermissionRequest,
  PermissionGrant,
  Permissioner,
} from './types.ts';
import type { Clock } from './clock.ts';

let grantSequence = 0;

interface PendingEntry {
  readonly request: ClipboardPermissionRequest;
  readonly resolve: (grant: PermissionGrant) => void;
  readonly reject: (error: Error) => void;
}

export class PermissionPromptSuspendedError extends Error {
  override readonly name = 'PermissionPromptSuspendedError';
}

/**
 * 可暂停的浏览器许可器测试替身。
 * 暂停时请求进入队列，不产生任何回调；恢复后也不会自动弹窗，
 * 必须由测试显式批准、拒绝或刷新，从而确定性地控制回调到达时机。
 * 每个请求得到独立的 grantId，授权单次消费，彼此不共享。
 */
export class PausablePermissioner implements Permissioner {
  private paused = true;
  private readonly pending: PendingEntry[] = [];
  private readonly consumed = new Set<string>();
  private readonly autoDecision = new Map<string, boolean>();

  /** 按 requestId 记录已发放的授权，供测试断言。 */
  readonly issuedGrants: PermissionGrant[] = [];
  /** 已消费授权 ID。 */
  readonly consumedGrantIds: string[] = [];
  /** 已作废（失败/取消）授权 ID 及原因。 */
  readonly invalidatedGrants: { grantId: string; reason: string }[] = [];
  /**
   * 异步门：测试可注入一个 Promise，让授权 resolve 后再让出一个事件循环回合，
   * 从而精确模拟“回调已到达、守卫复核前”的取消窗口。
   */
  resolutionGate: (() => Promise<void>) | null = null;

  private readonly clock: Clock;

  constructor(clock: Clock) {
    this.clock = clock;
  }

  pause(): void {
    this.paused = true;
  }

  resume(): void {
    this.paused = false;
  }

  isPaused(): boolean {
    return this.paused;
  }

  pendingCount(): number {
    return this.pending.length;
  }

  pendingRequestIds(): string[] {
    return this.pending.map((entry) => entry.request.requestId);
  }

  requestPermission(
    request: ClipboardPermissionRequest,
  ): Promise<PermissionGrant> {
    if (!this.paused) {
      const allowed = this.autoDecision.get(request.requestId);
      if (allowed === undefined) {
        throw new PermissionPromptSuspendedError(
          `no decision configured for request ${request.requestId} while running`,
        );
      }
      this.autoDecision.delete(request.requestId);
      if (!allowed) {
        return Promise.reject(
          new Error(`user denied clipboard ${request.mode} permission`),
        );
      }
      return Promise.resolve(this.buildGrant(request));
    }

    return new Promise<PermissionGrant>((resolve, reject) => {
      this.pending.push({
        request,
        resolve,
        reject,
      });
    });
  }

  /** 配置恢复模式下的即时决策（不经过暂停队列）。 */
  setAutoDecision(requestId: string, allowed: boolean): void {
    this.autoDecision.set(requestId, allowed);
  }

  grantNext(): PermissionGrant {
    const entry = this.takePending();
    const grant = this.buildGrant(entry.request);
    this.deliver(entry, grant);
    return grant;
  }

  grantRequest(requestId: string): PermissionGrant {
    const entry = this.takePending(requestId);
    const grant = this.buildGrant(entry.request);
    this.deliver(entry, grant);
    return grant;
  }

  denyNext(message = 'user denied the clipboard permission prompt'): void {
    const entry = this.takePending();
    entry.reject(new Error(message));
  }

  denyRequest(
    requestId: string,
    message = 'user denied the clipboard permission prompt',
  ): void {
    const entry = this.takePending(requestId);
    entry.reject(new Error(message));
  }

  /** 一次性批准（或拒绝）队列中的全部挂起请求，按入队顺序回调。 */
  flush(allowed: boolean): PermissionGrant[] {
    const grants: PermissionGrant[] = [];
    while (this.pending.length > 0) {
      const entry = this.pending.shift()!;
      if (allowed) {
        const grant = this.buildGrant(entry.request);
        grants.push(grant);
        this.deliver(entry, grant);
      } else {
        entry.reject(new Error('permission flushed as denied'));
      }
    }
    return grants;
  }

  markConsumed(grant: PermissionGrant): void {
    this.consumed.add(grant.grantId);
    this.consumedGrantIds.push(grant.grantId);
  }

  invalidateGrant(grant: PermissionGrant, reason: string): void {
    this.consumed.add(grant.grantId);
    this.invalidatedGrants.push({ grantId: grant.grantId, reason });
  }

  isConsumed(grantId: string): boolean {
    return this.consumed.has(grantId);
  }

  private buildGrant(request: ClipboardPermissionRequest): PermissionGrant {
    const grant: PermissionGrant = {
      grantId: `grant-${(grantSequence += 1)}`,
      requestId: request.requestId,
      mode: request.mode,
      snapshot: request.snapshot,
      issuedAt: this.clock.now(),
    };
    this.issuedGrants.push(grant);
    return grant;
  }

  private deliver(entry: PendingEntry, grant: PermissionGrant): void {
    if (this.resolutionGate) {
      this.resolutionGate().then(
        () => entry.resolve(grant),
        () => entry.resolve(grant),
      );
      return;
    }
    entry.resolve(grant);
  }

  private takePending(requestId?: string): PendingEntry {
    const index = requestId
      ? this.pending.findIndex((entry) => entry.request.requestId === requestId)
      : 0;
    if (index < 0) {
      throw new Error(
        requestId
          ? `no pending permission request ${requestId}`
          : 'no pending permission request',
      );
    }
    return this.pending.splice(index, 1)[0];
  }
}
