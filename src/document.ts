import {
  type Origin,
  isSameOrigin,
  originToString,
} from "./origin.ts";
import { requestsOriginKeyedAgentCluster } from "./agentCluster.ts";

/**
 * Portable stand-in for the platform `SecurityError` DOMException so the
 * library stays free of a DOM runtime while keeping the same name/message
 * semantics that browsers surface for a rejected `document.domain` setter.
 */
export class SecurityError extends Error {
  override readonly name = "SecurityError";
  constructor(message: string) {
    super(message);
  }
}

export interface DocumentOptions {
  /** The document's immutable tuple origin (scheme + host + port). */
  origin: Origin;
  /**
   * Raw `Origin-Agent-Cluster` response header value. Absent/`null` means the
   * document lives in a site-keyed agent cluster and retains the legacy
   * `document.domain` behavior.
   */
  originAgentClusterHeader?: string | null;
}

/**
 * A minimal, deterministic model of an HTML document focused on the
 * `document.domain` setter and the same-origin comparisons it influences.
 */
export class DocumentModel {
  readonly origin: Origin;

  /**
   * Whether the document was placed in an origin-keyed agent cluster because
   * its response carried `Origin-Agent-Cluster: ?1`.
   */
  readonly originKeyed: boolean;

  /**
   * The current value used for domain-based (relaxed) same-origin checks.
   * Starts equal to the origin host; a successful `setDomain` shortens it.
   */
  domain: string;

  /** Number of `setDomain` attempts, including failed ones. */
  domainAssignmentCount = 0;

  private readonly crossOriginIsolated: boolean;

  constructor(options: DocumentOptions) {
    this.origin = options.origin;
    this.originKeyed = requestsOriginKeyedAgentCluster(
      options.originAgentClusterHeader ?? null,
    );
    this.domain = options.origin.host;
    // Origin-keyed documents are never cross-origin isolated in this model;
    // the flag is retained to mirror the spec's COI guard ordering.
    this.crossOriginIsolated = false;
  }

  /**
   * Implements the HTML `document.domain` setter.
   *
   * In an origin-keyed agent cluster (response header `Origin-Agent-Cluster:
   * ?1`) the first guard fires: the assignment throws a `SecurityError`, the
   * origin tuple is untouched and `domain` keeps its previous value. Without
   * that header the legacy relaxation runs and a valid parent-domain
   * assignment shortens the document's effective domain.
   */
  setDomain(value: string): void {
    this.domainAssignmentCount += 1;

    // HTML: "If this's relevant global object's agent cluster's
    // is-origin-keyed is true, then throw a SecurityError."
    if (this.originKeyed) {
      throw new SecurityError(
        "Assignment to document.domain is forbidden in an origin-keyed " +
          "agent cluster (Origin-Agent-Cluster: ?1).",
      );
    }

    if (this.crossOriginIsolated) {
      throw new SecurityError(
        "Assignment to document.domain is forbidden in a cross-origin " +
          "isolated document.",
      );
    }

    const target = value.trim().toLowerCase();
    if (!this.isRegistrableDomainSuffix(target)) {
      throw new SecurityError(
        `"${value}" is not a parent domain of ${this.origin.host}.`,
      );
    }

    // Legacy path: relax the effective domain only. The origin tuple
    // (including the port) is never mutated.
    this.domain = target;
  }

  /**
   * Mirrors `document.domain` getter behavior: the effective domain, which a
   * successful setter can shorten to a parent domain.
   */
  getDomain(): string {
    return this.domain;
  }

  /**
   * Strict same-origin check on the immutable tuple: scheme, host and port
   * must match. Different ports are always distinct origins through this path.
   */
  isSameOrigin(other: DocumentModel): boolean {
    return isSameOrigin(this.origin, other.origin);
  }

  /**
   * The effective same-origin check used by DOM access.
   *
   * With the origin-keying header it is permanently the strict tuple
   * comparison (host + port), because the setter can never relax the domain.
   * Without the header it falls back to the legacy "same site domain" rule,
   * which ignores the port once both documents have shortened their domains
   * to a common parent.
   */
  canAccess(other: DocumentModel): boolean {
    if (this.originKeyed || other.originKeyed) {
      return isSameOrigin(this.origin, other.origin);
    }
    return this.isSameSiteDomain(other);
  }

  private isSameSiteDomain(other: DocumentModel): boolean {
    if (this.origin.scheme !== other.origin.scheme) {
      return false;
    }
    return this.domain === other.domain;
  }

  /**
   * A value is accepted by the legacy setter when it equals the current
   * effective domain or is a proper parent (registered-domain suffix) of it.
   */
  private isRegistrableDomainSuffix(candidate: string): boolean {
    if (candidate === "" || candidate.includes(":")) {
      return false;
    }
    if (candidate === this.domain) {
      return true;
    }
    const suffix = `.${candidate}`;
    return this.domain.endsWith(suffix);
  }

  toString(): string {
    return `${originToString(this.origin)} (domain=${this.domain}, ` +
      `originKeyed=${this.originKeyed})`;
  }
}
