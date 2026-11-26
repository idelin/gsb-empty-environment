/**
 * Cross-Origin-Embedder-Policy / Cross-Origin-Resource-Policy engine.
 *
 * Models the HTML "embedder policy" and Fetch "cross-origin resource policy
 * check" / "cross-origin embedder policy check" algorithms, including the
 * `credentialless` mode and full redirect-chain evaluation.
 */

import { allowsAnonymousCors } from './cors.ts';
import {
  isSameOrigin,
  opaqueOrigin,
  parseOrigin,
  relateOrigins,
  serializeOrigin,
  type Origin,
  type OriginRelationship,
} from './origin.ts';
import { parseSandbox, type SandboxTokens } from './coop.ts';

export type CoepValue = 'unsafe-none' | 'require-corp' | 'credentialless';
export type CorpValue = 'same-origin' | 'same-site' | 'cross-origin';

export interface CoepPolicy {
  readonly value: CoepValue;
  readonly reportOnly: CoepValue | null;
  readonly reportEndpoints: readonly string[];
}

export interface CorpPolicy {
  readonly value: CorpValue | null;
}

const COEP_VALUES: readonly CoepValue[] = [
  'unsafe-none',
  'require-corp',
  'credentialless',
];

const CORP_VALUES: readonly CorpValue[] = [
  'same-origin',
  'same-site',
  'cross-origin',
];

function parseCoepDirective(value: string | null | undefined): CoepValue {
  if (!value) return 'unsafe-none';
  const token = value.split(';', 1)[0].trim().toLowerCase();
  return (COEP_VALUES as readonly string[]).includes(token)
    ? (token as CoepValue)
    : 'unsafe-none';
}

export function parseCoep(headers: {
  readonly 'cross-origin-embedder-policy'?: string | null;
  readonly 'cross-origin-embedder-policy-report-only'?: string | null;
}): CoepPolicy {
  const value = parseCoepDirective(headers['cross-origin-embedder-policy']);
  const reportOnlyRaw = headers['cross-origin-embedder-policy-report-only'];
  const reportOnly = reportOnlyRaw ? parseCoepDirective(reportOnlyRaw) : null;
  const endpoints: string[] = [];
  for (const raw of [
    headers['cross-origin-embedder-policy'],
    headers['cross-origin-embedder-policy-report-only'],
  ]) {
    if (!raw) continue;
    for (const param of raw.split(';').slice(1)) {
      const match = /report-to\s*=\s*"?([^";]+)"?/i.exec(param);
      if (match) endpoints.push(match[1].trim());
    }
  }
  return { value, reportOnly, reportEndpoints: endpoints };
}

export function parseCorp(headerValue: string | null | undefined): CorpPolicy {
  if (!headerValue) return { value: null };
  const token = headerValue.split(';', 1)[0].trim().toLowerCase();
  return {
    value: (CORP_VALUES as readonly string[]).includes(token)
      ? (token as CorpValue)
      : null,
  };
}

/**
 * Fetch "cross-origin resource policy check". A missing or non-matching
 * CORP header blocks a no-cors response. CORS-mode responses are exempt
 * because the CORS protocol already gates readability.
 */
export function corpAllows(
  corp: CorpPolicy,
  relationship: OriginRelationship,
  requestMode: RequestMode,
): boolean {
  if (requestMode === 'cors') return true;
  if (corp.value === null) return false;
  if (corp.value === 'cross-origin') return true;
  if (corp.value === 'same-site') {
    return relationship === 'same-origin' || relationship === 'same-site';
  }
  return relationship === 'same-origin';
}

export type RequestMode =
  | 'navigate'
  | 'iframe'
  | 'no-cors'
  | 'cors'
  | 'websocket';

export type ResourceDest =
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
  | 'iframe'
  | 'document';

/** One hop of a redirect chain; every hop is evaluated independently. */
export interface ResourceHop {
  readonly url: string;
  readonly corp?: string | null;
  readonly 'access-control-allow-origin'?: string | null;
}

export interface SubresourceRequest {
  readonly embedderOrigin: string | Origin;
  readonly requestOrigin: string | Origin;
  readonly mode: Exclude<RequestMode, 'navigate' | 'iframe' | 'websocket'>;
  readonly destination: ResourceDest;
  /**
   * Whether the request would carry credentials (cookies). Under
   * `credentialless`, cross-origin no-cors requests are forced anonymous.
   */
  readonly credentials?: 'omit' | 'same-origin' | 'include';
  /** Final response, or full redirect chain in order. */
  readonly hops: readonly ResourceHop[];
}

export type BlockReason =
  | 'corp-missing'
  | 'corp-too-restrictive'
  | 'cors-not-allowed'
  | 'coep-iframe-not-embeddable'
  | 'opaque-origin-redirect'
  | 'none';

export interface EligibilityVerdict {
  readonly allowed: boolean;
  readonly reason: BlockReason;
  /** The specific hop that failed, when a redirect chain is involved. */
  readonly failedHop: number | null;
  /** Effective credentials mode after credentialless rewriting. */
  readonly effectiveCredentials: 'omit' | 'same-origin' | 'include';
  readonly details: string;
}

function allow(
  effectiveCredentials: EligibilityVerdict['effectiveCredentials'],
): EligibilityVerdict {
  return {
    allowed: true,
    reason: 'none',
    failedHop: null,
    effectiveCredentials,
    details: 'Subresource allowed by embedder policy',
  };
}

function block(
  reason: BlockReason,
  details: string,
  effectiveCredentials: EligibilityVerdict['effectiveCredentials'],
): EligibilityVerdict {
  return {
    allowed: false,
    reason,
    failedHop: null,
    effectiveCredentials,
    details,
  };
}

function evaluateHop(
  hop: ResourceHop,
  embedder: Origin,
  request: Origin,
  mode: SubresourceRequest['mode'],
  coep: CoepValue,
  credentials: NonNullable<SubresourceRequest['credentials']>,
): EligibilityVerdict {
  const relationship = relateOrigins(embedder, request);
  const sameOrigin = isSameOrigin(embedder, request);
  const corp = parseCorp(hop.corp);
  const effectiveCredentials: EligibilityVerdict['effectiveCredentials'] =
    coep === 'credentialless' && !sameOrigin ? 'omit' : credentials;

  if (coep === 'unsafe-none') {
    if (mode === 'no-cors' && !corpAllows(corp, relationship, mode)) {
      return block(
        corp.value === null ? 'corp-missing' : 'corp-too-restrictive',
        `CORP ${corp.value ?? 'absent'} forbids no-cors ${relationship} load`,
        effectiveCredentials,
      );
    }
    return allow(effectiveCredentials);
  }

  if (coep === 'require-corp') {
    if (sameOrigin) return allow(effectiveCredentials);
    if (mode === 'cors') {
      if (allowsAnonymousCors(embedder, hop['access-control-allow-origin'])) {
        return allow(effectiveCredentials);
      }
      return block(
        'cors-not-allowed',
        'Cross-origin CORS subresource missing matching ACAO under require-corp',
        effectiveCredentials,
      );
    }
    if (corpAllows(corp, relationship, mode)) return allow(effectiveCredentials);
    return block(
      corp.value === null ? 'corp-missing' : 'corp-too-restrictive',
      `require-corp blocks cross-origin no-cors response: CORP ${corp.value ?? 'absent'}`,
      effectiveCredentials,
    );
  }

  // credentialless
  if (sameOrigin) return allow(effectiveCredentials);
  if (mode === 'cors') {
    if (allowsAnonymousCors(embedder, hop['access-control-allow-origin'])) {
      return allow(effectiveCredentials);
    }
    return block(
      'cors-not-allowed',
      'Cross-origin CORS subresource missing matching ACAO under credentialless',
      effectiveCredentials,
    );
  }
  // Cross-origin no-cors media is fetched without credentials and exempted
  // from CORP, matching the credentialless "no-credentials fetch" rule.
  return allow(effectiveCredentials);
}

/**
 * Full COEP subresource eligibility over every redirect hop. A response is
 * allowed only when every hop passes; the first failing hop is reported.
 */
export function evaluateSubresource(
  request: SubresourceRequest,
  embedderCoep: CoepPolicy | CoepValue,
): EligibilityVerdict {
  const coep =
    typeof embedderCoep === 'string' ? embedderCoep : embedderCoep.value;
  const embedder =
    typeof request.embedderOrigin === 'string'
      ? parseOrigin(request.embedderOrigin)
      : request.embedderOrigin;
  const credentials = request.credentials ?? 'include';

  for (let index = 0; index < request.hops.length; index += 1) {
    const hop = request.hops[index];
    const hopOrigin = parseOrigin(hop.url);
    if (hopOrigin.opaque) {
      throw new Error(
        `Redirect hop ${index} URL ${hop.url} has an opaque origin; use ` +
          'evaluateRedirectChain with an explicit OpaqueOrigin instead',
      );
    }
    const verdict = evaluateHop(
      hop,
      embedder,
      hopOrigin,
      request.mode,
      coep,
      credentials,
    );
    if (!verdict.allowed) return { ...verdict, failedHop: index };
  }

  const crossesOrigin = request.hops.some(
    (hop) => !isSameOrigin(embedder, parseOrigin(hop.url)),
  );
  return {
    ...allow(
      coep === 'credentialless' && crossesOrigin ? 'omit' : credentials,
    ),
    failedHop: null,
  };
}

export interface RedirectChainStep {
  readonly url: string | Origin;
  readonly corp?: string | null;
  readonly 'access-control-allow-origin'?: string | null;
}

/**
 * Redirect-aware evaluation where individual hops may carry an explicit
 * opaque (sandbox-degenerated) origin. The moment the chain degenerates to
 * an opaque origin without an ACAO the gateway can validate, the load is
 * blocked — this is the `Origin: null` sandbox bypass defense.
 */
export function evaluateRedirectChain(
  request: Omit<SubresourceRequest, 'hops'> & {
    hops: readonly RedirectChainStep[];
  },
  embedderCoep: CoepPolicy | CoepValue,
): EligibilityVerdict {
  const coep =
    typeof embedderCoep === 'string' ? embedderCoep : embedderCoep.value;
  const embedder =
    typeof request.embedderOrigin === 'string'
      ? parseOrigin(request.embedderOrigin)
      : request.embedderOrigin;
  const credentials = request.credentials ?? 'include';

  for (let index = 0; index < request.hops.length; index += 1) {
    const step = request.hops[index];
    const hopOrigin =
      typeof step.url === 'string' ? parseOrigin(step.url) : step.url;

    if (hopOrigin.opaque) {
      const acao = step['access-control-allow-origin'];
      if (request.mode === 'cors' && allowsAnonymousCors(embedder, acao)) {
        continue;
      }
      return {
        allowed: false,
        reason: 'opaque-origin-redirect',
        failedHop: index,
        effectiveCredentials: 'omit',
        details:
          'Redirect chain degenerated into an opaque (null) origin inside a ' +
          'sandboxed context; no validated CORS header, so the embedder ' +
          'refuses the response',
      };
    }

    const verdict = evaluateHop(
      {
        url: serializeOrigin(hopOrigin),
        corp: step.corp,
        'access-control-allow-origin': step['access-control-allow-origin'],
      },
      embedder,
      hopOrigin,
      request.mode,
      coep,
      credentials,
    );
    if (!verdict.allowed) return { ...verdict, failedHop: index };
  }

  return { ...allow(credentials), failedHop: null };
}

export interface FrameEmbedRequest {
  readonly parentOrigin: string | Origin;
  readonly parentCoep: CoepPolicy | CoepValue;
  readonly childOrigin: string | Origin;
  readonly childCoep: CoepPolicy | CoepValue;
  readonly childCorp?: string | null;
  readonly childSandbox?: SandboxTokens;
}

export interface FrameEmbedVerdict {
  readonly allowed: boolean;
  readonly reason: BlockReason;
  readonly childCrossOriginIsolated: boolean;
  readonly credentiallessEmbedded: boolean;
  readonly details: string;
}

/**
 * HTML "check a navigation response's COEP" for an iframe navigation plus
 * the resulting child `crossOriginIsolated` state.
 */
export function evaluateFrameEmbed(request: FrameEmbedRequest): FrameEmbedVerdict {
  const parentCoep =
    typeof request.parentCoep === 'string'
      ? request.parentCoep
      : request.parentCoep.value;
  const childCoep =
    typeof request.childCoep === 'string'
      ? request.childCoep
      : request.childCoep.value;
  const parent =
    typeof request.parentOrigin === 'string'
      ? parseOrigin(request.parentOrigin)
      : request.parentOrigin;
  const child =
    typeof request.childOrigin === 'string'
      ? parseOrigin(request.childOrigin)
      : request.childOrigin;
  const sandbox = request.childSandbox ?? parseSandbox(null);
  const effectiveChild =
    sandbox.sandboxed && !sandbox.allowSameOrigin && !child.opaque
      ? opaqueOrigin(`sandbox:${child.host}`)
      : child;
  const sameOrigin = isSameOrigin(parent, effectiveChild);
  const corp = parseCorp(request.childCorp);
  const relationship = sameOrigin
    ? 'same-origin'
    : relateOrigins(parent, effectiveChild);

  if (parentCoep === 'unsafe-none') {
    return {
      allowed: true,
      reason: 'none',
      childCrossOriginIsolated: false,
      credentiallessEmbedded: false,
      details: 'Non-isolated parent imposes no embedder restrictions',
    };
  }

  if (sameOrigin) {
    return {
      allowed: true,
      reason: 'none',
      childCrossOriginIsolated: childCoep !== 'unsafe-none',
      credentiallessEmbedded: false,
      details:
        childCoep === 'unsafe-none'
          ? 'Same-origin child without COEP loads but breaks crossOriginIsolated'
          : 'Same-origin child joins the isolated agent cluster',
    };
  }

  // Cross-origin child under an embedding parent.
  if (parentCoep === 'require-corp') {
    const corpOk =
      corp.value === 'cross-origin' ||
      (corp.value === 'same-site' && relationship === 'same-site');
    if (!corpOk) {
      return {
        allowed: false,
        reason: 'coep-iframe-not-embeddable',
        childCrossOriginIsolated: false,
        credentiallessEmbedded: false,
        details: `require-corp parent: cross-origin iframe needs CORP cross-origin (got ${corp.value ?? 'absent'})`,
      };
    }
    if (childCoep !== 'require-corp') {
      return {
        allowed: false,
        reason: 'coep-iframe-not-embeddable',
        childCrossOriginIsolated: false,
        credentiallessEmbedded: false,
        details:
          'require-corp parent: cross-origin iframe must itself send COEP require-corp',
      };
    }
    return {
      allowed: true,
      reason: 'none',
      childCrossOriginIsolated: true,
      credentiallessEmbedded: false,
      details: 'Cross-origin child joins the isolated agent cluster',
    };
  }

  // credentialless parent
  if (childCoep === 'require-corp') {
    return {
      allowed: true,
      reason: 'none',
      childCrossOriginIsolated: true,
      credentiallessEmbedded: false,
      details:
        'COEP require-corp child opts back into the shared isolated cluster',
    };
  }
  return {
    allowed: true,
    reason: 'none',
    childCrossOriginIsolated: false,
    credentiallessEmbedded: true,
    details:
      'Cross-origin child without COEP is embedded anonymously without credentials',
  };
}

export interface IsolationDocument {
  readonly origin: string | Origin;
  readonly coop: import('./coop.ts').CoopPolicy | import('./coop.ts').CoopValue;
  readonly coep: CoepPolicy | CoepValue;
  readonly isTopLevel: boolean;
  /** For nested documents: the parent's effective isolation. */
  readonly parentIsolated?: boolean;
}

export interface IsolationVerdict {
  readonly crossOriginIsolated: boolean;
  readonly reasons: readonly string[];
}

function coopEnum(
  policy: import('./coop.ts').CoopPolicy | import('./coop.ts').CoopValue,
): import('./coop.ts').CoopValue {
  return typeof policy === 'string' ? policy : policy.value;
}

/**
 * Compute the `self.crossOriginIsolated` value for a document.
 *
 * Top level: COOP `same-origin` AND COEP (`require-corp` or
 * `credentialless`). Nested: additionally the whole ancestor chain must be
 * isolated and a cross-origin child must itself carry `require-corp`
 * (`credentialless` children are embedded, not isolated).
 */
export function evaluateCrossOriginIsolated(
  doc: IsolationDocument,
): IsolationVerdict {
  const reasons: string[] = [];
  const coop = coopEnum(doc.coop);
  const coep = typeof doc.coep === 'string' ? doc.coep : doc.coep.value;

  if (doc.isTopLevel) {
    if (coop !== 'same-origin') {
      reasons.push(
        `Top-level COOP is ${coop}; crossOriginIsolated requires same-origin`,
      );
    }
    if (coep === 'unsafe-none') {
      reasons.push(
        'Top-level COEP is unsafe-none; require-corp or credentialless is needed',
      );
    }
    return {
      crossOriginIsolated: coop === 'same-origin' && coep !== 'unsafe-none',
      reasons,
    };
  }

  if (!doc.parentIsolated) {
    reasons.push('Parent document is not cross-origin-isolated');
  }
  if (coep === 'unsafe-none') {
    reasons.push(
      'Nested document without COEP cannot be crossOriginIsolated',
    );
  }
  const crossOriginIsolated =
    doc.parentIsolated === true && coep === 'require-corp';
  if (!crossOriginIsolated && reasons.length === 0) {
    reasons.push(
      'Nested document must carry COEP require-corp to join the isolated cluster',
    );
  }
  return { crossOriginIsolated, reasons };
}
