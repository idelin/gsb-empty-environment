/**
 * Sec-Fetch-* resource isolation policy gate (server-side middleware logic).
 *
 * Implements the "resource isolation policy" pattern from the Fetch Metadata
 * Request Headers proposal: same-origin traffic always flows, top-level
 * user-activations are allowed narrowly, and cross-site subresource /
 * state-changing traffic is denied unless explicitly listed.
 */

import {
  isSameOrigin,
  isSameSite,
  parseOrigin,
  type Origin,
} from './origin.ts';

export type SecFetchSite =
  | 'same-origin'
  | 'same-site'
  | 'cross-site'
  | 'none';

export type SecFetchMode =
  | 'navigate'
  | 'nested-navigate'
  | 'cors'
  | 'no-cors'
  | 'websocket';

export type SecFetchDest =
  | 'document'
  | 'iframe'
  | 'empty'
  | 'script'
  | 'style'
  | 'image'
  | 'font'
  | 'audio'
  | 'video'
  | 'track'
  | 'object'
  | 'embed'
  | 'worker'
  | 'sharedworker'
  | 'websocket';

export interface FetchMetadataHeaders {
  readonly 'sec-fetch-site'?: SecFetchSite | string | null;
  readonly 'sec-fetch-mode'?: SecFetchMode | string | null;
  readonly 'sec-fetch-dest'?: SecFetchDest | string | null;
  readonly 'sec-fetch-user'?: '?1' | string | null;
  readonly origin?: string | null;
}

export type MetadataDecisionCode =
  | 'allow-same-origin'
  | 'allow-top-level-navigation'
  | 'allow-user-activation'
  | 'allow-explicit-cross-site'
  | 'allow-missing-metadata'
  | 'deny-csrf-state-change'
  | 'deny-cross-site-subresource'
  | 'deny-cross-site-navigation'
  | 'deny-null-origin-privilege'
  | 'deny-websocket-cross-site'
  | 'deny-malformed-metadata'
  | 'deny-missing-metadata';

export interface MetadataVerdict {
  readonly allow: boolean;
  readonly code: MetadataDecisionCode;
  readonly reason: string;
}

export interface MetadataPolicyConfig {
  /** Server origin used for independent cross-checks of the declared headers. */
  readonly serverOrigin: string;
  /**
   * Non-safelisted HTTP methods considered state-changing (CSRF surface).
   * Defaults to the standard set.
   */
  readonly stateChangingMethods?: readonly string[];
  /**
   * Explicit cross-site allow rules, e.g. public web font or image CDNs.
   * Each rule matches a destination (or '*') and same-site/cross-site scope.
   */
  readonly crossSiteAllowlist?: readonly CrossSiteAllowRule[];
  /**
   * How to treat clients that send no Sec-Fetch-* headers at all (older
   * browsers / non-browser agents). 'allow' is the migration default,
   * 'deny' is the hardened posture.
   */
  readonly missingMetadataMode?: 'allow' | 'deny';
  /**
   * Permit `Sec-Fetch-Site: none` top-level GET/HEAD navigations (typed
   * URLs, bookmarks, redirects out of opaque contexts). Defaults to true.
   */
  readonly allowNoneSiteNavigations?: boolean;
  /**
   * Treat any request whose `Origin` header is the literal `null` as
   * untrusted, even if Sec-Fetch-Site claims same-origin. Defaults to true.
   */
  readonly rejectNullOrigin?: boolean;
}

export interface CrossSiteAllowRule {
  readonly dest: SecFetchDest | '*';
  readonly methods?: readonly string[];
  readonly site?: 'same-site' | 'cross-site' | 'none';
}

const DEFAULT_STATE_CHANGING = [
  'post',
  'put',
  'patch',
  'delete',
];

const VALID_SITES: readonly SecFetchSite[] = [
  'same-origin',
  'same-site',
  'cross-site',
  'none',
];

const VALID_MODES: readonly SecFetchMode[] = [
  'navigate',
  'nested-navigate',
  'cors',
  'no-cors',
  'websocket',
];

function deny(code: MetadataDecisionCode, reason: string): MetadataVerdict {
  return { allow: false, code, reason };
}

function allow(code: MetadataDecisionCode, reason: string): MetadataVerdict {
  return { allow: true, code, reason };
}

/**
 * Independently derive what `Sec-Fetch-Site` should be for a request so a
 * spoofed or stripped header cannot overstate trust.
 */
export function deriveSecFetchSite(
  serverOrigin: string | Origin,
  requestOrigin: string | Origin | null | undefined,
): SecFetchSite {
  if (requestOrigin === null || requestOrigin === undefined) return 'none';
  const parsed =
    typeof requestOrigin === 'string' ? parseOrigin(requestOrigin) : requestOrigin;
  if (parsed.opaque) return 'none';
  const server =
    typeof serverOrigin === 'string' ? parseOrigin(serverOrigin) : serverOrigin;
  if (isSameOrigin(server, parsed)) return 'same-origin';
  if (isSameSite(server, parsed)) return 'same-site';
  return 'cross-site';
}

export interface MetadataRequest {
  readonly method: string;
  readonly headers: FetchMetadataHeaders;
}

/**
 * Main gate. Returns an allow/deny verdict with a stable decision code the
 * host middleware can log and map onto a 403 response.
 */
export function evaluateFetchMetadata(
  request: MetadataRequest,
  config: MetadataPolicyConfig,
): MetadataVerdict {
  const method = request.method.toUpperCase();
  const stateChanging = new Set(
    (config.stateChangingMethods ?? DEFAULT_STATE_CHANGING).map((value) =>
      value.toUpperCase(),
    ),
  );
  const headers = request.headers;
  const declaredSite = headers['sec-fetch-site'] ?? null;
  const mode = headers['sec-fetch-mode'] ?? null;
  const dest = headers['sec-fetch-dest'] ?? null;
  const userActivated = headers['sec-fetch-user'] === '?1';
  const rejectNull = config.rejectNullOrigin ?? true;

  // Opaque/null origins are never trusted, regardless of other claims.
  if (rejectNull && headers.origin?.trim().toLowerCase() === 'null') {
    return deny(
      'deny-null-origin-privilege',
      'Origin: null (sandboxed iframe / data: URL / redirect-degenerated) ' +
        'cannot inherit ambient privilege',
    );
  }

  // Requests without any fetch metadata at all.
  if (declaredSite === null && mode === null && dest === null) {
    if ((config.missingMetadataMode ?? 'allow') === 'deny') {
      return deny(
        'deny-missing-metadata',
        'Hardened policy rejects clients that omit Sec-Fetch-* metadata',
      );
    }
    return allow(
      'allow-missing-metadata',
      'Non-browser or legacy client without fetch metadata',
    );
  }

  if (
    (declaredSite !== null && !VALID_SITES.includes(declaredSite as SecFetchSite)) ||
    (mode !== null && !VALID_MODES.includes(mode as SecFetchMode))
  ) {
    return deny(
      'deny-malformed-metadata',
      `Malformed Sec-Fetch headers: site=${String(declaredSite)} mode=${String(mode)}`,
    );
  }

  const site = (declaredSite ?? deriveSecFetchSite(config.serverOrigin, headers.origin)) as
    | SecFetchSite
    | null;

  // Cross-check the declared site against the actual Origin when present.
  if (headers.origin && headers.origin.trim().toLowerCase() !== 'null') {
    const derived = deriveSecFetchSite(config.serverOrigin, headers.origin);
    if (site !== null && derived !== site) {
      return deny(
        'deny-malformed-metadata',
        `Declared Sec-Fetch-Site ${site} contradicts derived ${derived}`,
      );
    }
  }

  if (site === 'same-origin') {
    return allow('allow-same-origin', 'Same-origin request');
  }

  const explicitRule = (config.crossSiteAllowlist ?? []).find((rule) => {
    const destMatch = rule.dest === '*' || rule.dest === dest;
    const methodMatch =
      !rule.methods ||
      rule.methods.map((value) => value.toUpperCase()).includes(method);
    const siteMatch = !rule.site || rule.site === site;
    return destMatch && methodMatch && siteMatch;
  });
  if (explicitRule) {
    return allow(
      'allow-explicit-cross-site',
      'Matched explicit cross-site resource isolation allowlist rule',
    );
  }

  // WebSocket handshakes are upgrade requests; cross-site ones need opt-in.
  if (mode === 'websocket' || dest === 'websocket') {
    return deny(
      'deny-websocket-cross-site',
      'Cross-site WebSocket handshake denied without explicit allow rule',
    );
  }

  const topLevelNavigation =
    mode === 'navigate' && (dest === 'document' || dest === null);

  if (site === 'none') {
    if (
      topLevelNavigation &&
      !stateChanging.has(method) &&
      (config.allowNoneSiteNavigations ?? true)
    ) {
      return allow(
        'allow-top-level-navigation',
        'Top-level navigation with no initiator (typed URL / bookmark)',
      );
    }
    if (userActivated && topLevelNavigation && !stateChanging.has(method)) {
      return allow(
        'allow-user-activation',
        'User-activated top-level navigation',
      );
    }
    return deny(
      'deny-cross-site-subresource',
      'Site=none request is neither a safe top-level navigation nor allowlisted',
    );
  }

  // same-site (cross-origin subdomain) and cross-site share the strict path.
  if (topLevelNavigation) {
    if (stateChanging.has(method)) {
      return deny(
        'deny-csrf-state-change',
        `Cross-site state-changing ${method} navigation blocked (CSRF)`,
      );
    }
    if (userActivated) {
      return allow(
        'allow-user-activation',
        'User-activated cross-site top-level GET navigation',
      );
    }
    return deny(
      'deny-cross-site-navigation',
      'Cross-site top-level navigation without user activation is denied in strict mode',
    );
  }

  // iframe nesting is a nested navigation, not a top-level one.
  if (mode === 'nested-navigate' || dest === 'iframe') {
    return deny(
      'deny-cross-site-subresource',
      'Cross-site iframe embedding denied without an explicit allow rule',
    );
  }

  if (stateChanging.has(method)) {
    return deny(
      'deny-csrf-state-change',
      `Cross-site state-changing ${method} request blocked (CSRF)`,
    );
  }

  return deny(
    'deny-cross-site-subresource',
    `Cross-site ${String(dest ?? mode)} subresource probe denied (XS-Leak surface)`,
  );
}
