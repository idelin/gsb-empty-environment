/**
 * ECH (Encrypted Client Hello) identity verifier.
 *
 * Consumed from the TLS handshake-result callback of an ECH-enabled client.
 * It decides, for each handshake attempt, whether the connection may proceed,
 * whether a retry with server-provided retry_configs is allowed, or whether
 * the connection must be aborted.
 *
 * Security invariants enforced here:
 *  - ECH accepted: the peer certificate MUST be valid for the inner SNI.
 *    A certificate that only covers public_name is treated as an attack
 *    (a front-end impersonating the inner origin).
 *  - ECH rejected: the peer certificate is verified ONLY against
 *    public_name. retry_configs are adopted only if that verification
 *    passes, and the rejected connection must never carry application data.
 *  - At most one ECH retry is permitted.
 *  - Falling back to a handshake without ECH is never permitted.
 */

export interface PeerCertificate {
  /** DNS names (SAN dNSName entries) the certificate is valid for. */
  readonly dnsNames: readonly string[];
}

export interface EchHandshakeOutcome {
  /** True if the server accepted ECH (decrypted the inner ClientHello). */
  readonly echAccepted: boolean;
  /** Certificate presented by the peer on this connection. */
  readonly certificate: PeerCertificate;
  /** ECHConfigList supplied by the server on rejection (retry_configs). */
  readonly retryConfigs?: Uint8Array;
}

export type EchDecision =
  | { readonly action: 'proceed' }
  | {
      readonly action: 'retry';
      readonly retryConfigs: Uint8Array;
      /** Always false: a rejected connection must not carry application data. */
      readonly allowApplicationData: false;
    }
  | { readonly action: 'abort'; readonly reason: string };

export interface EchIdentityConfig {
  /** The real origin name carried in the inner ClientHello SNI. */
  readonly innerServerName: string;
  /** The public_name carried in the outer ClientHello SNI. */
  readonly publicName: string;
  /** Maximum number of ECH retries. Defaults to 1. */
  readonly maxRetries?: number;
}

/**
 * Returns true if the certificate is valid for `name`, supporting a single
 * left-most wildcard label (e.g. "*.example.com" matches "a.example.com"
 * but not "example.com" or "a.b.example.com").
 */
export function certificateMatchesName(
  certificate: PeerCertificate,
  name: string,
): boolean {
  const target = name.toLowerCase();
  return certificate.dnsNames.some((pattern) => {
    const candidate = pattern.toLowerCase();
    if (candidate.startsWith('*.')) {
      const suffix = candidate.slice(1); // ".example.com"
      if (!target.endsWith(suffix)) return false;
      const label = target.slice(0, target.length - suffix.length);
      return label.length > 0 && !label.includes('.');
    }
    return candidate === target;
  });
}

export class EchIdentityVerifier {
  private readonly config: EchIdentityConfig;
  private readonly maxRetries: number;
  private retriesUsed = 0;
  private currentConnectionMayCarryAppData = false;

  constructor(config: EchIdentityConfig) {
    this.config = config;
    this.maxRetries = config.maxRetries ?? 1;
  }

  /**
   * Called from the handshake-result callback once per connection attempt.
   * Returns the decision for that connection.
   */
  handleHandshakeResult(outcome: EchHandshakeOutcome): EchDecision {
    this.currentConnectionMayCarryAppData = false;

    if (outcome.echAccepted) {
      if (
        certificateMatchesName(outcome.certificate, this.config.innerServerName)
      ) {
        this.currentConnectionMayCarryAppData = true;
        return { action: 'proceed' };
      }
      return {
        action: 'abort',
        reason:
          'ECH was accepted but the certificate is not valid for the inner ' +
          'SNI; the peer may be a front-end impersonating the origin',
      };
    }

    // ECH rejected: authenticate only against public_name. A certificate
    // valid for the inner name proves nothing here and must not be trusted.
    if (!certificateMatchesName(outcome.certificate, this.config.publicName)) {
      return {
        action: 'abort',
        reason:
          'ECH was rejected and the certificate is not valid for ' +
          'public_name; retry_configs are discarded',
      };
    }

    if (this.retriesUsed >= this.maxRetries) {
      return {
        action: 'abort',
        reason: `ECH retry limit reached (${this.maxRetries}); refusing further retries`,
      };
    }

    if (!outcome.retryConfigs || outcome.retryConfigs.length === 0) {
      return {
        action: 'abort',
        reason:
          'ECH was rejected without retry_configs; falling back to a ' +
          'handshake without ECH is forbidden',
      };
    }

    this.retriesUsed += 1;
    return {
      action: 'retry',
      retryConfigs: outcome.retryConfigs,
      allowApplicationData: false,
    };
  }

  /**
   * Guard to call before sending application data on the current connection.
   * Throws unless the most recent handshake was accepted and authenticated.
   */
  assertApplicationDataAllowed(): void {
    if (!this.currentConnectionMayCarryAppData) {
      throw new Error(
        'application data must not be sent on this connection: ECH was not ' +
          'accepted with a certificate valid for the inner SNI',
      );
    }
  }
}
