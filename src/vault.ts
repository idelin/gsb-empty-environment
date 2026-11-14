import { AuditLog } from './audit.ts';
import type { CallerContext } from './context.ts';

/** 保险库策略：限流与权限要求。 */
export interface VaultPolicy {
  /** 每次用户手势最多允许的明文读取次数（默认 1）。 */
  readonly maxRevealsPerGesture: number;
  /** 滑动窗口内最大读取次数（默认 5）。 */
  readonly maxRevealsPerWindow: number;
  /** 滑动窗口长度，毫秒（默认 60_000）。 */
  readonly windowMs: number;
}

export const DEFAULT_POLICY: VaultPolicy = Object.freeze({
  maxRevealsPerGesture: 1,
  maxRevealsPerWindow: 5,
  windowMs: 60_000,
});

/** 建议的响应头 / iframe 策略，供部署侧使用。 */
export const RECOMMENDED_PERMISSIONS_POLICY = 'credential-isolation=(self)';

export type DenyReason =
  | 'insecure-context'
  | 'cross-origin-frame'
  | 'third-party-script'
  | 'permissions-policy'
  | 'no-user-gesture'
  | 'rate-limit-gesture'
  | 'rate-limit-window'
  | 'unknown-credential';

export class CredentialAccessDenied extends Error {
  readonly reason: DenyReason;
  constructor(reason: DenyReason) {
    super(`credential access denied: ${reason}`);
    this.name = 'CredentialAccessDenied';
    this.reason = reason;
  }
}

/**
 * 受保护的填充目标。组件只通过 setInternalValue 写入，
 * 永不触碰 DOM content attribute，保证 outerHTML / getAttribute 不可见。
 */
export interface SecretSink {
  setInternalValue(value: string): void;
}

/** 把 <input> 包装为 SecretSink：仅写 IDL value 属性，不写 content attribute。 */
export function inputSink(input: { value: string }): SecretSink {
  return { setInternalValue: (v) => { input.value = v; } };
}

interface SealedRecord {
  readonly iv: Uint8Array;
  readonly ciphertext: ArrayBuffer;
}

/**
 * 凭据隔离保险库。
 *
 * - 明文只在内存中以 AES-GCM 密文形式落盘，密钥不可导出；
 * - 明文读取必须同时满足：受保护上下文 + 瞬时用户手势 + 第一方脚本
 *   + 同源顶层框架 + Permissions-Policy 放行；
 * - 每次手势限读次数 + 滑动窗口限流，杜绝批量导出；
 * - 不提供任何批量读取 / 枚举明文的 API；
 * - 全部允许与拒绝决策写入防篡改审计日志。
 */
export class CredentialVault {
  readonly policy: VaultPolicy;
  #key: CryptoKey;
  #store = new Map<string, SealedRecord>();
  #audit = new AuditLog();
  #gestureUse = new Map<string, number>();
  #revealTimes: number[] = [];

  private constructor(key: CryptoKey, policy: VaultPolicy) {
    this.#key = key;
    this.policy = policy;
  }

  static async create(policy: Partial<VaultPolicy> = {}): Promise<CredentialVault> {
    const key = await crypto.subtle.generateKey(
      { name: 'AES-GCM', length: 256 },
      false, // 不可导出
      ['encrypt', 'decrypt'],
    );
    return new CredentialVault(key, { ...DEFAULT_POLICY, ...policy });
  }

  get audit(): AuditLog {
    return this.#audit;
  }

  /** 存储凭据（仅存密文）。 */
  async store(id: string, secret: string): Promise<void> {
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const ciphertext = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv },
      this.#key,
      new TextEncoder().encode(secret),
    );
    this.#store.set(id, { iv, ciphertext });
    await this.#audit.record('store', id, true, 'sealed');
  }

  /** 手势内读取明文。违反任一约束即拒绝并审计。 */
  async reveal(id: string, ctx: CallerContext): Promise<string> {
    await this.#authorize(id, ctx);
    const rec = this.#store.get(id);
    if (!rec) {
      await this.#deny(id, 'unknown-credential');
    }
    this.#consumeRevealQuota(ctx);
    await this.#audit.record('reveal', id, true, 'ok');
    const plain = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: rec!.iv },
      this.#key,
      rec!.ciphertext,
    );
    return new TextDecoder().decode(plain);
  }

  /**
   * 自动填充：明文经组件内部直接写入受保护 sink，
   * 不经过 DOM content attribute，页面脚本无法通过
   * getAttribute / outerHTML / 序列化读到。
   */
  async autofill(id: string, sink: SecretSink, ctx: CallerContext): Promise<void> {
    const secret = await this.reveal(id, ctx);
    sink.setInternalValue(secret);
    await this.#audit.record('autofill', id, true, 'filled-into-protected-sink');
  }

  async #authorize(id: string, ctx: CallerContext): Promise<void> {
    if (!ctx.secureContext) return this.#deny(id, 'insecure-context');
    if (!ctx.sameOriginFrame) return this.#deny(id, 'cross-origin-frame');
    if (!ctx.firstPartyScript) return this.#deny(id, 'third-party-script');
    if (!ctx.policyAllows) return this.#deny(id, 'permissions-policy');
    if (!ctx.userActivation) return this.#deny(id, 'no-user-gesture');

    const gid = ctx.gestureId ?? '__anonymous__';
    const used = this.#gestureUse.get(gid) ?? 0;
    if (used >= this.policy.maxRevealsPerGesture) {
      return this.#deny(id, 'rate-limit-gesture');
    }
    const now = Date.now();
    this.#revealTimes = this.#revealTimes.filter((t) => now - t < this.policy.windowMs);
    if (this.#revealTimes.length >= this.policy.maxRevealsPerWindow) {
      return this.#deny(id, 'rate-limit-window');
    }
  }

  #consumeRevealQuota(ctx: CallerContext): void {
    const gid = ctx.gestureId ?? '__anonymous__';
    this.#gestureUse.set(gid, (this.#gestureUse.get(gid) ?? 0) + 1);
    this.#revealTimes.push(Date.now());
  }

  async #deny(id: string, reason: DenyReason): Promise<never> {
    await this.#audit.record('deny', id, false, reason);
    throw new CredentialAccessDenied(reason);
  }
}
