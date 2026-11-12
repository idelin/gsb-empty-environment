/**
 * ECH 身份判定组件：供握手结果回调使用。
 *
 * 安全不变量：
 * - ECH 被接受时，证书必须对内层 SNI 有效，否则中止；
 * - ECH 被拒绝时，只能按 public_name 验证证书，通过后才可采用 retry_configs，
 *   且该连接上不得发送任何应用数据；
 * - 重试至多一次，绝不回落到不带 ECH 的连接。
 */

export interface PeerCertificate {
  readonly dnsNames: readonly string[];
}

export type CertificateVerifier = (
  certificate: PeerCertificate,
  expectedName: string,
) => boolean;

/** RFC 6125 风格的主机名匹配：精确匹配（忽略大小写）与单层通配符。 */
export function matchHostname(pattern: string, hostname: string): boolean {
  const p = pattern.toLowerCase();
  const h = hostname.toLowerCase();
  if (p.startsWith("*.")) {
    const suffix = p.slice(1); // ".example.com"
    if (!h.endsWith(suffix)) return false;
    const label = h.slice(0, h.length - suffix.length);
    return label.length > 0 && !label.includes(".");
  }
  return p === h;
}

export const defaultCertificateVerifier: CertificateVerifier = (
  certificate,
  expectedName,
) => certificate.dnsNames.some((name) => matchHostname(name, expectedName));

export type EchHandshakeResult =
  | {
      readonly echAccepted: true;
      readonly peerCertificate: PeerCertificate;
    }
  | {
      readonly echAccepted: false;
      readonly peerCertificate: PeerCertificate;
      readonly retryConfigs?: unknown;
    };

export type EchDecision =
  | { readonly action: "proceed" }
  | { readonly action: "retry"; readonly retryConfigs: unknown }
  | { readonly action: "abort"; readonly reason: string };

export interface EchConnectionPlan {
  /** 永不回落：后续连接一律携带 ECH。 */
  readonly ech: true;
  readonly retryConfigs: unknown;
}

export interface EchIdentityGuardOptions {
  /** 内层 SNI（真实域名）。 */
  readonly innerSni: string;
  /** 外层 ClientHello 中的 public_name。 */
  readonly publicName: string;
  readonly verifyCertificate?: CertificateVerifier;
}

export class EchIdentityGuard {
  static readonly MAX_RETRIES = 1;

  readonly #innerSni: string;
  readonly #publicName: string;
  readonly #verify: CertificateVerifier;
  #retriesUsed = 0;
  #applicationDataAllowed = false;

  constructor(options: EchIdentityGuardOptions) {
    this.#innerSni = options.innerSni;
    this.#publicName = options.publicName;
    this.#verify = options.verifyCertificate ?? defaultCertificateVerifier;
  }

  get retriesRemaining(): number {
    return EchIdentityGuard.MAX_RETRIES - this.#retriesUsed;
  }

  /** 握手结果回调：对一次完成的握手作出身份判定。 */
  onHandshakeComplete(result: EchHandshakeResult): EchDecision {
    this.#applicationDataAllowed = false;

    if (result.echAccepted) {
      if (this.#verify(result.peerCertificate, this.#innerSni)) {
        this.#applicationDataAllowed = true;
        return { action: "proceed" };
      }
      return {
        action: "abort",
        reason:
          `ECH accepted but peer certificate is not valid for inner SNI ` +
          `"${this.#innerSni}"`,
      };
    }

    if (this.#retriesUsed >= EchIdentityGuard.MAX_RETRIES) {
      return {
        action: "abort",
        reason:
          "ECH rejected after the retry budget was exhausted; " +
          "falling back to a non-ECH connection is forbidden",
      };
    }

    if (!this.#verify(result.peerCertificate, this.#publicName)) {
      return {
        action: "abort",
        reason:
          `ECH rejected and peer certificate is not valid for public_name ` +
          `"${this.#publicName}"; retry_configs must not be adopted`,
      };
    }

    if (result.retryConfigs == null) {
      return {
        action: "abort",
        reason: "ECH rejected without retry_configs",
      };
    }

    this.#retriesUsed += 1;
    return { action: "retry", retryConfigs: result.retryConfigs };
  }

  /** 发送应用数据前必须调用；ECH 被拒绝的连接上一律抛出。 */
  assertApplicationDataAllowed(): void {
    if (!this.#applicationDataAllowed) {
      throw new Error(
        "application data must not be sent on a connection where ECH was not accepted",
      );
    }
  }

  /** 依据上一次判定规划下一条连接；返回 null 表示不得再连接。 */
  nextConnectionPlan(decision: EchDecision): EchConnectionPlan | null {
    if (decision.action !== "retry") return null;
    return { ech: true, retryConfigs: decision.retryConfigs };
  }
}
