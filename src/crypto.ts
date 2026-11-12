import type { KeyCommitmentEntry, PrivateStateToken } from './types.ts';

/** 针对单枚令牌的验证结果。 */
export interface VerifyOutcome {
  valid: boolean;
  /** 验证失败原因，仅用于日志/审计。 */
  reason?: string;
}

/**
 * 令牌密码验证器（依赖注入）。
 *
 * 组件不内置任何具体算法，真实服务可对接 WebCrypto / BoringSSL PMB 验签，
 * 测试可注入可控的假实现以覆盖重放、伪造等路径。
 */
export interface TokenVerifier {
  verify(token: PrivateStateToken, key: KeyCommitmentEntry): Promise<VerifyOutcome>;
}

/**
 * 测试用验证器：维护一份“合法签名”集合，并支持强制拒绝列表。
 * 生产代码不要使用。
 */
export class StaticTokenVerifier implements TokenVerifier {
  private readonly validSignatures = new Set<string>();
  private readonly reject = new Set<string>();

  /** 登记一枚令牌的签名为合法。 */
  accept(token: PrivateStateToken): this {
    this.validSignatures.add(encode(token.signature));
    return this;
  }

  /** 强制某签名验证失败（模拟伪造/损坏令牌）。 */
  rejectSignature(signature: Uint8Array): this {
    this.reject.add(encode(signature));
    return this;
  }

  async verify(token: PrivateStateToken, key: KeyCommitmentEntry): Promise<VerifyOutcome> {
    const sig = encode(token.signature);
    if (this.reject.has(sig)) {
      return { valid: false, reason: 'signature blacklisted' };
    }
    if (!this.validSignatures.has(sig)) {
      return { valid: false, reason: 'signature not recognized' };
    }
    if (token.keyId !== key.keyId) {
      return { valid: false, reason: 'key id mismatch' };
    }
    return { valid: true };
  }
}

export function encode(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export function decode(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i += 1) {
    out[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}
