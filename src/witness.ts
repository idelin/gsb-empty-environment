import { Network } from './network.ts';
import type { AcquireResponse, Lease, WitnessStatus } from './types.ts';

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

export interface WitnessOptions {
  network: Network;
  now?: () => number;
}

/**
 * 第三方法官（Witness）：跨机房全局写入租约的唯一仲裁者。
 *
 * 安全不变式：
 *  1. 任意时刻最多颁发一份未过期租约；
 *  2. term（fencing token）全局单调递增，旧租约持有者的写凭证必然失效；
 *  3. 接管（takeover）前必须 (a) 亲自探测确认现持有者失联，
 *     且 (b) 等待旧租约自然过期后才颁发新租约 —— 新旧租约的有效期绝不重叠，
 *     从时间上彻底杜绝双机房双写。
 *
 * RPC 路由：
 *   POST /lease/acquire   { site, ttlMs }                    -> AcquireResponse
 *   POST /lease/takeover  { site, ttlMs, observedTerm }      -> AcquireResponse
 *   POST /lease/validate  { site, term }                     -> { valid }
 *   GET  /lease/status                                       -> WitnessStatus
 */
export class Witness {
  private lease: Lease | null = null;
  private term = 0;
  private readonly now: () => number;
  private readonly opts: WitnessOptions;
  /** 串行化所有变更操作，避免并发 takeover 交错。 */
  private queue: Promise<unknown> = Promise.resolve();

  constructor(opts: WitnessOptions) {
    this.opts = opts;
    this.now = opts.now ?? (() => Date.now());
  }

  /** 把法官挂到网络上（端点名默认 "witness"）。 */
  attach(name = 'witness'): void {
    this.opts.network.register(name, (path, method, body) => this.route(path, method, body));
  }

  private serialize<T>(fn: () => Promise<T>): Promise<T> {
    const p = this.queue.then(fn);
    this.queue = p.catch(() => {});
    return p;
  }

  private async route(path: string, method: string, body?: unknown): Promise<unknown> {
    const b = (body ?? {}) as Record<string, unknown>;
    if (method === 'GET' && path === '/lease/status') {
      return this.status();
    }
    if (method === 'POST' && path === '/lease/acquire') {
      return await this.serialize(async () => this.handleAcquire(String(b.site), Number(b.ttlMs)));
    }
    if (method === 'POST' && path === '/lease/takeover') {
      return await this.serialize(async () =>
        this.handleTakeover(String(b.site), Number(b.ttlMs), Number(b.observedTerm)),
      );
    }
    if (method === 'POST' && path === '/lease/validate') {
      return await this.serialize(async () => ({
        valid: this.handleValidate(String(b.site), Number(b.term)),
      }));
    }
    throw new Error(`witness: unknown route ${method} ${path}`);
  }

  private grant(site: string, ttlMs: number): Lease {
    this.term += 1;
    this.lease = { holder: site, term: this.term, expiresAt: this.now() + ttlMs };
    return this.lease;
  }

  /** 法官独立探测某机房控制器是否存活。 */
  private async probe(site: string): Promise<boolean> {
    try {
      await this.opts.network.call('witness', site, '/health');
      return true;
    } catch {
      return false;
    }
  }

  /** 申请或续租：仅当租约空闲/已过期/由本人持有时颁发。 */
  private handleAcquire(site: string, ttlMs: number): AcquireResponse {
    const now = this.now();
    if (!this.lease || this.lease.expiresAt <= now || this.lease.holder === site) {
      return { granted: true, lease: this.grant(site, ttlMs) };
    }
    return { granted: false, lease: this.lease, reason: 'held-by-other' };
  }

  /**
   * 强制接管：备机房在主机房失联时请求晋升。
   * 法官必须亲自确认持有者真正失联，并等待旧租约到期后才颁发新租约。
   */
  private async handleTakeover(site: string, ttlMs: number, observedTerm: number): Promise<AcquireResponse> {
    const now = this.now();
    if (!this.lease || this.lease.expiresAt <= now) {
      return { granted: true, lease: this.grant(site, ttlMs) };
    }
    if (this.lease.holder === site) {
      return { granted: true, lease: this.lease };
    }
    if (observedTerm !== this.lease.term) {
      return { granted: false, lease: this.lease, reason: 'stale-observed-term' };
    }
    // (a) 法官独立确认现持有者真正失联，而非仅凭申请者一面之词
    const holderAlive = await this.probe(this.lease.holder);
    if (holderAlive) {
      return { granted: false, lease: this.lease, reason: 'holder-alive' };
    }
    // (b) 等待旧租约自然过期：旧持有者即使活着，其本地租约也必然已失效
    const waitMs = this.lease.expiresAt - this.now();
    if (waitMs > 0) await sleep(waitMs);
    // 等待期间租约可能已被他人取走，需复核
    if (this.lease && this.lease.expiresAt > this.now() && this.lease.holder !== site) {
      return { granted: false, lease: this.lease, reason: 'lease-changed' };
    }
    return { granted: true, lease: this.grant(site, ttlMs) };
  }

  /** fencing 校验：写入方必须持有当前 term 的未过期租约。 */
  private handleValidate(site: string, term: number): boolean {
    return (
      this.lease !== null &&
      this.lease.holder === site &&
      this.lease.term === term &&
      this.lease.expiresAt > this.now()
    );
  }

  status(): WitnessStatus {
    return { now: this.now(), lease: this.lease };
  }
}
