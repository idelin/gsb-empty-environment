import { Network } from './network.ts';
import type { AcquireResponse, Lease, Role } from './types.ts';

export interface ControllerOptions {
  site: string;
  peer: string;
  network: Network;
  /** 法官在网络上的端点名。 */
  witnessName?: string;
  /** 租约 TTL（ms）。主机必须在其到期前完成续租，否则自动停写。 */
  ttlMs: number;
  now?: () => number;
}

/**
 * 机房级租约控制器：驱动 主/备/隔离 状态机，并对每次写入做 fencing 校验。
 *
 * 状态机：
 *   standby --(租约空闲/接管成功)--> primary
 *   primary --(续租被拒：凭证已失效)--> fenced（只读，需人工 reset）
 *   primary --(本地租约到期且无法续租)--> standby（停止写入）
 *
 * 写入安全：role 必须是 primary、本地租约未过期、且法官确认 term 仍然有效，
 * 三者同时满足才允许写；任一不满足即只读（fail-closed）。
 *
 * 管理路由：GET /health、GET /state、POST /write {key,value}
 */
export class LeaseController {
  role: Role = 'standby';
  lease: Lease | null = null;
  readonly store = new Map<string, string>();
  private readonly now: () => number;
  private readonly opts: ControllerOptions;
  private readonly witnessName: string;
  private timer: NodeJS.Timeout | null = null;

  constructor(opts: ControllerOptions) {
    this.opts = opts;
    this.witnessName = opts.witnessName ?? 'witness';
    this.now = opts.now ?? (() => Date.now());
  }

  get site(): string {
    return this.opts.site;
  }

  /** 把控制器挂到网络上（端点名 = site 名）。 */
  attach(): void {
    this.opts.network.register(this.opts.site, (path, method, body) => this.route(path, method, body));
  }

  private async route(path: string, method: string, body?: unknown): Promise<unknown> {
    if (method === 'GET' && path === '/health') {
      return { site: this.opts.site, role: this.role };
    }
    if (method === 'GET' && path === '/state') {
      return {
        site: this.opts.site,
        role: this.role,
        lease: this.lease,
        store: Object.fromEntries(this.store),
      };
    }
    if (method === 'POST' && path === '/write') {
      const b = (body ?? {}) as Record<string, unknown>;
      const term = await this.write(String(b.key), String(b.value));
      return { ok: true, term };
    }
    throw new Error(`controller ${this.opts.site}: unknown route ${method} ${path}`);
  }

  /** 本地持有的租约是否仍在有效期内（不依赖法官可达性）。 */
  private localLeaseValid(): boolean {
    return this.lease !== null && this.lease.expiresAt > this.now();
  }

  private becomePrimary(lease: Lease): void {
    this.role = 'primary';
    this.lease = lease;
  }

  /** 凭证被法官判为失效：降级为只读隔离态，绝不自动重新竞选。 */
  private fenceOff(): void {
    this.role = 'fenced';
    this.lease = null;
  }

  /** 人工恢复：解除隔离，重新以普通备机身份参与竞选。 */
  resetToStandby(): void {
    if (this.role === 'fenced') this.role = 'standby';
  }

  private async witnessAcquire(): Promise<AcquireResponse> {
    return await this.opts.network.call<AcquireResponse>(
      this.opts.site, this.witnessName, '/lease/acquire', 'POST',
      { site: this.opts.site, ttlMs: this.opts.ttlMs },
    );
  }

  private async witnessTakeover(observedTerm: number): Promise<AcquireResponse> {
    return await this.opts.network.call<AcquireResponse>(
      this.opts.site, this.witnessName, '/lease/takeover', 'POST',
      { site: this.opts.site, ttlMs: this.opts.ttlMs, observedTerm },
    );
  }

  private async peerAlive(): Promise<boolean> {
    try {
      await this.opts.network.call(this.opts.site, this.opts.peer, '/health');
      return true;
    } catch {
      return false;
    }
  }

  /** 状态机推进一步：续租 / 竞选 / 接管 / 到期降级。 */
  async tick(): Promise<void> {
    if (this.role === 'primary') {
      try {
        const res = await this.witnessAcquire();
        if (res.granted && res.lease && res.lease.holder === this.opts.site) {
          this.lease = res.lease;
        } else {
          // 法官已把租约颁给别人：本机凭证失效，立即隔离为只读
          this.fenceOff();
          return;
        }
      } catch {
        // 法官不可达（如光纤挖断）：在本地租约到期前保持角色，到期即停写
      }
      if (!this.localLeaseValid()) {
        this.role = 'standby';
        this.lease = null;
      }
      return;
    }

    if (this.role === 'fenced') return; // 隔离态：只读，等待人工恢复

    // standby：先尝试正常获取（租约空闲或已过期时直接成功）
    let res: AcquireResponse;
    try {
      res = await this.witnessAcquire();
    } catch {
      return; // 法官不可达，保持只读
    }
    if (res.granted && res.lease) {
      this.becomePrimary(res.lease);
      return;
    }
    // 租约被 peer 持有：若 peer 失联则请求法官仲裁接管
    if (res.lease && res.lease.holder !== this.opts.site && !(await this.peerAlive())) {
      try {
        const t = await this.witnessTakeover(res.lease.term);
        if (t.granted && t.lease) this.becomePrimary(t.lease);
      } catch {
        // 接管期间与法官失联，保持只读，下个 tick 重试
      }
    }
  }

  /** 启动周期循环。 */
  start(intervalMs = 50): void {
    if (this.timer) return;
    this.timer = setInterval(() => void this.tick(), intervalMs);
    this.timer.unref();
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  /**
   * 写入入口。三重校验：本机角色、本地租约有效期、法官 fencing token。
   * 任何一项不满足都拒绝写入（只读）。
   */
  async write(key: string, value: string): Promise<number> {
    if (this.role !== 'primary' || !this.localLeaseValid()) {
      throw new Error(`[${this.opts.site}] read-only: not an active primary`);
    }
    const { valid } = await this.opts.network.call<{ valid: boolean }>(
      this.opts.site, this.witnessName, '/lease/validate', 'POST',
      { site: this.opts.site, term: this.lease!.term },
    );
    if (!valid) {
      // 法官否认了我们的 term：凭证已失效，立即隔离
      this.fenceOff();
      throw new Error(`[${this.opts.site}] fenced: lease term rejected by witness`);
    }
    this.store.set(key, value);
    return this.lease!.term;
  }

  read(key: string): string | undefined {
    return this.store.get(key);
  }

  close(): void {
    this.stop();
    this.opts.network.unregister(this.opts.site);
  }
}
