/**
 * Server-side CORS response validation and preflight hardening.
 *
 * Focuses on the misconfigurations that enable cross-origin credential
 * theft: wildcard-with-credentials, unnormalized reflected origins, opaque
 * `null` reflection, and missing `Vary: Origin`.
 */

import {
  isSameOrigin,
  originMatchesAllowlist,
  parseOrigin,
  serializeOrigin,
  type Origin,
} from './origin.ts';

export interface CorsResponseHeaders {
  readonly 'access-control-allow-origin'?: string | null;
  readonly 'access-control-allow-credentials'?: string | null;
  readonly 'access-control-allow-headers'?: string | null;
  readonly 'access-control-allow-methods'?: string | null;
  readonly 'access-control-max-age'?: string | null;
  readonly 'access-control-expose-headers'?: string | null;
  readonly vary?: string | null;
}

export type CorsViolationCode =
  | 'wildcard-with-credentials'
  | 'reflected-origin-unnormalized'
  | 'opaque-null-reflection'
  | 'origin-not-allowlisted'
  | 'wildcard-cannot-credential'
  | 'missing-vary-origin'
  | 'preflight-wildcard-headers-with-credentials'
  | 'invalid-header-value';

export interface CorsViolation {
  readonly code: CorsViolationCode;
  readonly message: string;
  readonly header: string;
}

export interface CorsVerdict {
  readonly allowed: boolean;
  /** Effective ACAO the gateway should emit (null means suppress the header). */
  readonly effectiveAllowOrigin: string | null;
  readonly credentialsAllowed: boolean;
  readonly violations: readonly CorsViolation[];
}

function isTruthy(value: string | null | undefined): boolean {
  return typeof value === 'string' && value.trim().toLowerCase() === 'true';
}

function splitHeaderList(value: string | null | undefined): string[] {
  if (!value) return [];
  return value
    .split(',')
    .map((token) => token.trim().toLowerCase())
    .filter(Boolean);
}

export interface CorsPolicyConfig {
  /**
   * Exact origins permitted to read credentialed responses. Every entry is
   * parsed and normalized before comparison; substring reflection is never
   * accepted.
   */
  readonly allowedOrigins: Iterable<string>;
  /** Allow `Access-Control-Allow-Credentials: true` at all. */
  readonly allowCredentials?: boolean;
  /**
   * Require `Vary: Origin` whenever the ACAO value is dynamically reflected
   * (cache-poisoning defense). Defaults to enabled.
   */
  readonly requireVaryOrigin?: boolean;
  /**
   * Permit reflecting the literal opaque `null` origin. Defaults to false;
   * sandboxed iframes and redirect chains otherwise inherit `Origin: null`
   * and get credentialed access they must never have.
   */
  readonly allowOpaqueNull?: boolean;
}

/**
 * Validate a (possibly attacker-influenced) CORS response against the
 * gateway policy. The request's `Origin` header is taken from the actual
 * request, never from the response body.
 */
export function validateCorsResponse(
  requestOrigin: string | null | undefined,
  headers: CorsResponseHeaders,
  config: CorsPolicyConfig,
): CorsVerdict {
  const violations: CorsViolation[] = [];
  const allowCredentials = config.allowCredentials ?? false;
  const requireVary = config.requireVaryOrigin ?? true;
  const allowOpaqueNull = config.allowOpaqueNull ?? false;

  const acao = headers['access-control-allow-origin']?.trim() ?? null;
  const acac = isTruthy(headers['access-control-allow-credentials']);

  if (acao === null) {
    return {
      allowed: false,
      effectiveAllowOrigin: null,
      credentialsAllowed: false,
      violations: [
        {
          code: 'invalid-header-value',
          message: 'Response carries no Access-Control-Allow-Origin header',
          header: 'access-control-allow-origin',
        },
      ],
    };
  }

  // Hard spec violation: `*` + credentials can never be honored together.
  if (acao === '*' && acac) {
    violations.push({
      code: 'wildcard-with-credentials',
      message:
        'Access-Control-Allow-Origin: * must not coexist with ' +
        'Access-Control-Allow-Credentials: true (Fetch CORS check fails and ' +
        'browsers reject the response outright)',
      header: 'access-control-allow-origin',
    });
    return finalize(false, null, false, violations);
  }

  if (acao === '*') {
    // Reaching here means acac was false: anonymous public wildcard is valid
    // but the response can never be read with credentials.
    return finalize(
      true,
      '*',
      false,
      violations,
    );
  }

  // Opaque/null origin handling — the sandbox-iframe / redirect-chain bypass.
  if (acao.toLowerCase() === 'null') {
    if (!allowOpaqueNull) {
      violations.push({
        code: 'opaque-null-reflection',
        message:
          'Reflecting Access-Control-Allow-Origin: null grants credentialed ' +
          'access to sandboxed iframes and documents whose Origin degenerated ' +
          'to null across a redirect chain',
        header: 'access-control-allow-origin',
      });
    }
    return finalize(false, null, false, violations);
  }

  let reflected: Origin;
  try {
    reflected = parseOrigin(acao);
  } catch {
    violations.push({
      code: 'invalid-header-value',
      message: `Unparseable Access-Control-Allow-Origin value: ${JSON.stringify(acao)}`,
      header: 'access-control-allow-origin',
    });
    return finalize(false, null, acac, violations);
  }
  if (reflected.opaque) {
    violations.push({
      code: 'opaque-null-reflection',
      message: 'Access-Control-Allow-Origin resolved to an opaque origin',
      header: 'access-control-allow-origin',
    });
    return finalize(false, null, false, violations);
  }

  // Reflected origins must match the request exactly after normalization;
  // echoing whatever the client sent is only safe against the allowlist.
  const allowlisted = originMatchesAllowlist(serializeOrigin(reflected), [
    ...config.allowedOrigins,
  ]);
  if (!allowlisted) {
    violations.push({
      code: 'origin-not-allowlisted',
      message: `Reflected origin ${serializeOrigin(reflected)} is not in the normalized allowlist`,
      header: 'access-control-allow-origin',
    });
  }

  let matchesRequest = false;
  if (requestOrigin && requestOrigin.trim().toLowerCase() !== 'null') {
    try {
      const request = parseOrigin(requestOrigin);
      matchesRequest = !request.opaque && isSameOrigin(request, reflected);
    } catch {
      matchesRequest = false;
    }
  } else if (requestOrigin?.trim().toLowerCase() === 'null') {
    if (!allowOpaqueNull) {
      violations.push({
        code: 'opaque-null-reflection',
        message:
          'Request Origin degenerated to null (sandboxed iframe or redirect ' +
          'chain); credentialed reflection must be refused',
        header: 'access-control-allow-origin',
      });
    }
  }

  if (!matchesRequest && requestOrigin) {
    violations.push({
      code: 'reflected-origin-unnormalized',
      message:
        'ACAO value does not equal the normalized request origin; raw string ' +
        'reflection without tuple comparison is forbidden',
      header: 'access-control-allow-origin',
    });
  }

  if (matchesRequest && requireVary) {
    const vary = splitHeaderList(headers.vary);
    if (!vary.includes('origin')) {
      violations.push({
        code: 'missing-vary-origin',
        message:
          'Reflected ACAO requires Vary: Origin so shared caches do not serve ' +
          'one origin\'s response to another',
        header: 'vary',
      });
    }
  }

  if (acac && !allowCredentials) {
    violations.push({
      code: 'wildcard-cannot-credential',
      message: 'Policy disallows Access-Control-Allow-Credentials entirely',
      header: 'access-control-allow-credentials',
    });
  }

  const hardBlocked = violations.some(
    (violation) =>
      violation.code === 'wildcard-with-credentials' ||
      violation.code === 'opaque-null-reflection' ||
      violation.code === 'origin-not-allowlisted' ||
      violation.code === 'reflected-origin-unnormalized' ||
      violation.code === 'invalid-header-value',
  );
  const effectiveAcao = hardBlocked ? null : serializeOrigin(reflected);
  return finalize(!hardBlocked, effectiveAcao, acac && allowCredentials, violations);
}

/**
 * Preflight (OPTIONS) validation. In addition to the ACAO/ACAC rules, a
 * wildcard `Access-Control-Allow-Headers: *` cannot authorize credentialed
 * requests for `Authorization`.
 */
export function validatePreflight(
  requestOrigin: string | null | undefined,
  headers: CorsResponseHeaders,
  config: CorsPolicyConfig,
  requestedHeaders: readonly string[] = [],
): CorsVerdict {
  const verdict = validateCorsResponse(requestOrigin, headers, config);
  const violations = [...verdict.violations];
  const acah = splitHeaderList(headers['access-control-allow-headers']);
  const credentialed = isTruthy(headers['access-control-allow-credentials']);

  if (credentialed && acah.includes('*')) {
    const needsAuth = requestedHeaders
      .map((header) => header.toLowerCase())
      .includes('authorization');
    if (needsAuth) {
      violations.push({
        code: 'preflight-wildcard-headers-with-credentials',
        message:
          'Access-Control-Allow-Headers: * never covers Authorization on a ' +
          'credentialed request; enumerate headers explicitly',
        header: 'access-control-allow-headers',
      });
    }
  }

  return { ...verdict, violations, allowed: verdict.allowed && violations.length === 0 };
}

/**
 * Low-level anonymous CORS test used by the COEP embedder model: the
 * response is readable when ACAO is `*` or exactly equals the normalized
 * request origin. Credentials are irrelevant because credentialless / COEP
 * subresource requests strip them.
 */
export function allowsAnonymousCors(
  requestOrigin: string | Origin,
  acao: string | null | undefined,
): boolean {
  if (!acao) return false;
  const value = acao.trim();
  if (value === '*') return true;
  if (value.toLowerCase() === 'null') return false;
  try {
    return isSameOrigin(requestOrigin, parseOrigin(value));
  } catch {
    return false;
  }
}

function finalize(
  allowed: boolean,
  effectiveAllowOrigin: string | null,
  credentialsAllowed: boolean,
  violations: CorsViolation[],
): CorsVerdict {
  return { allowed, effectiveAllowOrigin, credentialsAllowed, violations };
}
