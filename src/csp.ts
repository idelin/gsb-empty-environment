/**
 * CSP Level 3 policy parsing, intersection and bypass auditing.
 *
 * Zero runtime dependencies. Implements the security-relevant parts of
 * Content Security Policy Level 3 (https://www.w3.org/TR/CSP3/):
 *
 *  - multiple policies are enforced simultaneously (logical AND / intersection);
 *  - duplicate directives inside one policy: only the first one is honored;
 *  - directive fallback to `default-src` (e.g. `script-src` / `object-src`);
 *  - `script-src-elem` -> `script-src` -> `default-src` resolution chain;
 *  - `'strict-dynamic'` semantics: host-source / scheme-source / `'unsafe-inline'`
 *    expressions are ignored when a nonce/hash is present;
 *  - nonce and hash (sha256/sha384/sha512) matching for inline scripts;
 *  - bypass-oriented auditing (unsafe keywords, wildcards, JSONP / open redirect
 *    hosts, missing `object-src 'none'`, missing `base-uri`, ...).
 */

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export type Severity = 'high' | 'medium' | 'low' | 'info';

export interface CspFinding {
  /** Stable machine readable code. */
  code:
    | 'DUPLICATE_DIRECTIVE'
    | 'UNSAFE_INLINE'
    | 'UNSAFE_EVAL'
    | 'WILDCARD_SCRIPT'
    | 'WILDCARD_SCOPE'
    | 'BROAD_SCHEME_SCRIPT'
    | 'DATA_SCRIPT'
    | 'UNSAFE_HOST_SCRIPT'
    | 'STRICT_DYNAMIC_WITHOUT_NONCE_OR_HASH'
    | 'MISSING_OBJECT_SRC'
    | 'OBJECT_SRC_UNSAFE'
    | 'MISSING_BASE_URI'
    | 'BASE_URI_UNSAFE'
    | 'JSONP_OR_REDIRECT_SOURCE'
    | 'MISSING_DEFAULT_OR_SCRIPT_SRC';
  severity: Severity;
  message: string;
  /** 0-based policy index among the parsed headers. */
  policyIndex: number;
  /** Directive name when the finding is attached to one. */
  directive?: string;
  /** Offending source expression when applicable. */
  source?: string;
}

export type SourceKind =
  | 'none'
  | 'wildcard'
  | 'self'
  | 'nonce'
  | 'hash'
  | 'strict-dynamic'
  | 'unsafe-inline'
  | 'unsafe-eval'
  | 'keyword'
  | 'scheme'
  | 'host';

export interface CspSource {
  raw: string;
  kind: SourceKind;
  /** Lower-cased scheme without trailing colon, e.g. `data`, `https`. */
  scheme?: string;
  /** Lower-cased host for host / wildcard-host sources. */
  host?: string;
  /** True for host sources like `*.example.com` (any sub-domain, not apex). */
  hostWildcard?: boolean;
  nonce?: string;
  /** Hash algorithm (`sha256` ...). */
  hashAlgorithm?: string;
  /** Base64 hash value. */
  hashValue?: string;
}

export interface CspDirective {
  name: string;
  value: string;
  tokens: string[];
  sources: CspSource[];
}

export interface ParsedPolicy {
  /** Raw header value. */
  raw: string;
  /** First occurrence of every directive (later duplicates are ignored). */
  directives: Map<string, CspDirective>;
  /** Duplicated directives, including the ignored values. */
  duplicates: { name: string; ignoredValue: string }[];
}

export interface EffectiveScriptSources {
  /** Directive that ultimately provided the sources. */
  resolvedFrom: 'script-src-elem' | 'script-src' | 'default-src' | undefined;
  sources: CspSource[];
  hasStrictDynamic: boolean;
  hasNonceOrHash: boolean;
  nonces: string[];
  hashes: { algorithm: string; value: string }[];
}

export interface CspAuditOptions {
  /** Additional host names (or suffixes) treated as JSONP / redirect sinks. */
  extraRiskyHosts?: string[];
}

export interface CspAuditResult {
  policies: ParsedPolicy[];
  findings: CspFinding[];
  /** Strictest-intersection view used for evaluation against every header. */
  merged: Map<string, CspDirective>;
}

export interface InlineScriptResult {
  allowed: boolean;
  /** Per-policy decision trace. */
  reasons: { policyIndex: number; allowed: boolean; reason: string }[];
}

// ---------------------------------------------------------------------------
// Known JSONP / open-redirect sinks (CSP Evaluator-style list)
// ---------------------------------------------------------------------------

/**
 * Hosts that historically expose JSONP endpoints or open redirectors. Allowing
 * any of them in a script directive effectively re-introduces script injection
 * even without `'unsafe-inline'`.
 */
export const KNOWN_RISKY_HOSTS: readonly string[] = [
  'www.google.com',
  'ajax.googleapis.com',
  'raw.githubusercontent.com',
  'gist.githubusercontent.com',
  'pastebin.com',
  'paste.ee',
  'jsfiddle.net',
  'jsbin.com',
  'codepen.io',
  'www.googletagmanager.com',
  'www.google-analytics.com',
  'connect.facebook.net',
  'platform.twitter.com',
  'cdnjs.cloudflare.com',
  'stackpath.bootstrapcdn.com',
  'maxcdn.bootstrapcdn.com',
  'unpkg.com',
  'cdn.jsdelivr.net',
  'www.youtube.com',
  's3.amazonaws.com',
  'storage.googleapis.com',
  'onedrive.live.com',
  '1drv.ms',
  'drive.google.com',
  'docs.google.com',
  'reddit.com',
  'www.reddit.com',
  'bit.ly',
  'tinyurl.com',
  't.co',
  'goo.gl',
  'ow.ly',
  'is.gd',
  'buff.ly',
  'gravatar.com',
  'api.jquery.com',
  'code.jquery.com',
  'yastatic.net',
  'mc.yandex.ru',
  'tagmanager.google.com',
  'maps.googleapis.com',
  'translate.google.com',
  'accounts.google.com',
  'vk.com',
  'www.paypalobjects.com',
];

const HASH_ALGORITHMS = new Set(['sha256', 'sha384', 'sha512']);

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

/** Parse a single CSP header value into a policy (duplicate-aware). */
export function parsePolicy(raw: string): ParsedPolicy {
  const directives = new Map<string, CspDirective>();
  const duplicates: { name: string; ignoredValue: string }[] = [];

  for (const segment of raw.split(';')) {
    const tokens = segment
      .split(/[\s]+/)
      .map((token) => token.trim())
      .filter((token) => token.length > 0);
    if (tokens.length === 0) continue;

    const name = tokens[0].toLowerCase();
    const valueTokens = tokens.slice(1);
    if (directives.has(name)) {
      // CSP3: duplicate directives are ignored entirely, the first one wins.
      duplicates.push({ name, ignoredValue: valueTokens.join(' ') });
      continue;
    }
    directives.set(name, {
      name,
      value: valueTokens.join(' '),
      tokens: valueTokens,
      sources: valueTokens.map(parseSource),
    });
  }

  return { raw, directives, duplicates };
}

/** Parse one or more CSP header values (each header becomes one policy). */
export function parsePolicies(headers: string | string[]): ParsedPolicy[] {
  const list = Array.isArray(headers) ? headers : [headers];
  return list.map((header) => parsePolicy(header));
}

/** Classify a single source-expression token. */
export function parseSource(token: string): CspSource {
  const raw = token;
  const lower = token.toLowerCase();

  if (lower === "'none'") return { raw, kind: 'none' };
  if (lower === '*') return { raw, kind: 'wildcard' };
  if (lower === "'self'") return { raw, kind: 'self' };
  if (lower === "'strict-dynamic'") return { raw, kind: 'strict-dynamic' };
  if (lower === "'unsafe-inline'") return { raw, kind: 'unsafe-inline' };
  if (lower === "'unsafe-eval'") return { raw, kind: 'unsafe-eval' };

  if (token.startsWith("'") && token.endsWith("'") && token.length > 2) {
    const inner = token.slice(1, -1);
    const dash = inner.indexOf('-');
    if (dash > 0) {
      const head = inner.slice(0, dash).toLowerCase();
      const tail = inner.slice(dash + 1);
      if (head === 'nonce' && tail.length > 0) {
        return { raw, kind: 'nonce', nonce: tail };
      }
      if (HASH_ALGORITHMS.has(head) && tail.length > 0) {
        return { raw, kind: 'hash', hashAlgorithm: head, hashValue: tail };
      }
    }
    return { raw, kind: 'keyword' };
  }

  // Scheme source: `data:`, `https:`, `http:`, ...
  const schemeMatch = /^([a-z][a-z0-9+.-]*):$/i.exec(token);
  if (schemeMatch) {
    return { raw, kind: 'scheme', scheme: schemeMatch[1].toLowerCase() };
  }

  return parseHostSource(raw);
}

function parseHostSource(raw: string): CspSource {
  let hostWildcard = false;
  let work = raw;

  if (work.startsWith('*.')) {
    hostWildcard = true;
    work = work.slice(2);
  }

  let url: URL | undefined;
  try {
    if (work.startsWith('//') || /^[a-z][a-z0-9+.-]*:\/\//i.test(work)) {
      url = new URL(work.startsWith('//') ? `https:${work}` : work);
    } else {
      url = new URL(`https://${work}`);
    }
  } catch {
    url = undefined;
  }

  const scheme = /^([a-z][a-z0-9+.-]*):\/\//i.exec(raw)?.[1]?.toLowerCase();
  if (url) {
    return {
      raw,
      kind: 'host',
      scheme,
      host: url.hostname.toLowerCase(),
      hostWildcard,
    };
  }
  return { raw, kind: 'host', scheme, host: work.toLowerCase(), hostWildcard };
}

// ---------------------------------------------------------------------------
// Directive resolution / fallback chains
// ---------------------------------------------------------------------------

const FALLBACK_CHAINS: Record<string, string[]> = {
  // CSP3: `script-src-elem` falls back to `script-src` then `default-src`.
  'script-src-elem': ['script-src', 'default-src'],
  'script-src-attr': ['script-src', 'default-src'],
  'script-src': ['default-src'],
  'style-src-elem': ['style-src', 'default-src'],
  'style-src-attr': ['style-src', 'default-src'],
  'style-src': ['default-src'],
  'child-src': ['default-src'],
  'worker-src': ['child-src', 'default-src'],
  'manifest-src': ['default-src'],
  'connect-src': ['default-src'],
  'img-src': ['default-src'],
  'font-src': ['default-src'],
  'media-src': ['default-src'],
  'object-src': ['default-src'],
  // Note: base-uri, form-action and frame-ancestors intentionally have NO fallback.
};

/** Resolve a directive following its CSP3 fallback chain. */
export function resolveDirective(
  policy: ParsedPolicy,
  directive: string,
): CspDirective | undefined {
  const direct = policy.directives.get(directive.toLowerCase());
  if (direct) return direct;
  for (const fallback of FALLBACK_CHAINS[directive.toLowerCase()] ?? []) {
    const candidate = policy.directives.get(fallback);
    if (candidate) return candidate;
  }
  return undefined;
}

/**
 * Resolve effective script sources applying the
 * `script-src-elem` -> `script-src` -> `default-src` chain and
 * `'strict-dynamic'` semantics.
 */
export function resolveScriptSources(policy: ParsedPolicy): EffectiveScriptSources {
  const elem = policy.directives.get('script-src-elem');
  const script = policy.directives.get('script-src');
  const fallback = policy.directives.get('default-src');

  let resolvedFrom: EffectiveScriptSources['resolvedFrom'];
  let directive: CspDirective | undefined;
  if (elem) {
    resolvedFrom = 'script-src-elem';
    directive = elem;
  } else if (script) {
    resolvedFrom = 'script-src';
    directive = script;
  } else if (fallback) {
    resolvedFrom = 'default-src';
    directive = fallback;
  }

  const all = directive?.sources ?? [];
  const hasStrictDynamic = all.some((source) => source.kind === 'strict-dynamic');
  const nonces = all.filter((s) => s.kind === 'nonce').map((s) => s.nonce!);
  const hashes = all
    .filter((s) => s.kind === 'hash')
    .map((s) => ({ algorithm: s.hashAlgorithm!, value: s.hashValue! }));
  const hasNonceOrHash = nonces.length > 0 || hashes.length > 0;

  let sources = all;
  if (hasStrictDynamic) {
    // CSP3: the presence of 'strict-dynamic' always makes host-source,
    // scheme-source and wildcard expressions ignored. 'unsafe-inline' is
    // additionally ignored once a nonce/hash provides a trusted root. Nonces,
    // hashes, 'strict-dynamic' itself and 'unsafe-eval' remain in effect.
    sources = all.filter((source) => {
      if (source.kind === 'host' || source.kind === 'scheme') return false;
      if (source.kind === 'wildcard') return false;
      if (source.kind === 'unsafe-inline' && hasNonceOrHash) return false;
      return true;
    });
  }

  return { resolvedFrom, sources, hasStrictDynamic, hasNonceOrHash, nonces, hashes };
}

// ---------------------------------------------------------------------------
// Multi-header intersection
// ---------------------------------------------------------------------------

/**
 * Merge several policies into their strictest intersection. Browsers enforce
 * every CSP header simultaneously (logical AND), so a directive expression is
 * only effective when it appears in every policy. Fallback chains are applied
 * before intersecting; directives missing in any policy are dropped (the
 * effective policy then has no allowance for them).
 */
export function intersectPolicies(policies: ParsedPolicy[]): Map<string, CspDirective> {
  const merged = new Map<string, CspDirective>();
  if (policies.length === 0) return merged;
  if (policies.length === 1) {
    return new Map(policies[0].directives);
  }

  const directiveNames = new Set<string>();
  for (const policy of policies) {
    for (const name of policy.directives.keys()) directiveNames.add(name);
  }

  for (const name of directiveNames) {
    const perPolicy: CspDirective[] = [];
    let presentInAll = true;
    for (const policy of policies) {
      const resolved =
        policy.directives.get(name) ?? resolveDirective(policy, name);
      if (!resolved) {
        presentInAll = false;
        break;
      }
      perPolicy.push(resolved);
    }
    if (!presentInAll) continue;

    // Intersect raw source tokens (case-insensitive keyword comparison).
    const common = perPolicy[0].tokens.filter((token) =>
      perPolicy
        .slice(1)
        .every((directive) =>
          directive.tokens.some(
            (other) => other.toLowerCase() === token.toLowerCase(),
          ),
        ),
    );

    merged.set(name, {
      name,
      value: common.join(' '),
      tokens: common,
      sources: common.map(parseSource),
    });
  }

  return merged;
}

// ---------------------------------------------------------------------------
// Nonce / hash matching
// ---------------------------------------------------------------------------

function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  // btoa is available in both browsers and Node 16+.
  return btoa(binary);
}

async function shaDigest(algorithm: string, content: string): Promise<string> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) {
    throw new Error('WebCrypto subtle crypto is unavailable in this host');
  }
  const data = new TextEncoder().encode(content);
  const webCryptoAlgorithm = algorithm.toUpperCase().replace('SHA', 'SHA-');
  const digest = await subtle.digest(webCryptoAlgorithm, data);
  return toBase64(new Uint8Array(digest));
}

function normalizeInlineContent(content: string): string {
  // CSP hashes are computed over the exact script text; callers pass the raw
  // element text. Leading/trailing whitespace handling mirrors browsers which
  // hash the exact text node content.
  return content;
}

/**
 * Decide whether an inline `<script>` body is allowed by every policy.
 * Checks nonce first, then sha256/sha384/sha512 hashes; honors
 * `'unsafe-inline'` only when no nonce/hash/strict-dynamic overrides it.
 */
export async function evaluateInlineScript(
  policies: ParsedPolicy[],
  content: string,
  nonce?: string,
): Promise<InlineScriptResult> {
  const hashesByAlgorithm = new Map<string, string>();
  const reasons: InlineScriptResult['reasons'] = [];

  const needAlgorithms = new Set<string>();
  for (const policy of policies) {
    for (const hash of resolveScriptSources(policy).hashes) {
      needAlgorithms.add(hash.algorithm);
    }
  }
  for (const algorithm of needAlgorithms) {
    hashesByAlgorithm.set(
      algorithm,
      await shaDigest(algorithm, normalizeInlineContent(content)),
    );
  }

  policies.forEach((policy, policyIndex) => {
    const effective = resolveScriptSources(policy);

    if (nonce && effective.nonces.includes(nonce)) {
      reasons.push({ policyIndex, allowed: true, reason: 'nonce match' });
      return;
    }

    const hashMatch = effective.hashes.some(
      (hash) => hashesByAlgorithm.get(hash.algorithm) === hash.value,
    );
    if (hashMatch) {
      reasons.push({ policyIndex, allowed: true, reason: 'hash match' });
      return;
    }

    if (effective.hasStrictDynamic) {
      reasons.push({
        policyIndex,
        allowed: false,
        reason:
          "'strict-dynamic' ignores 'unsafe-inline'; nonce/hash required for inline script",
      });
      return;
    }

    if (effective.hasNonceOrHash) {
      reasons.push({
        policyIndex,
        allowed: false,
        reason: 'nonce/hash present makes inline allowlist keywords ignored',
      });
      return;
    }

    if (effective.sources.some((source) => source.kind === 'unsafe-inline')) {
      reasons.push({ policyIndex, allowed: true, reason: "'unsafe-inline' allowed" });
      return;
    }

    reasons.push({
      policyIndex,
      allowed: false,
      reason: `no matching nonce/hash and no 'unsafe-inline' (resolved via ${
        effective.resolvedFrom ?? 'no directive'
      })`,
    });
  });

  return { allowed: reasons.every((reason) => reason.allowed), reasons };
}

// ---------------------------------------------------------------------------
// Bypass auditing
// ---------------------------------------------------------------------------

function isRiskyHost(host: string | undefined, riskyHosts: readonly string[]): boolean {
  if (!host) return false;
  const normalized = host.toLowerCase();
  return riskyHosts.some(
    (risky) => normalized === risky || normalized.endsWith(`.${risky}`),
  );
}

function directiveIsNone(directive: CspDirective | undefined): boolean {
  return !!directive && directive.sources.some((source) => source.kind === 'none');
}

function directiveAllowsOnlySelfOrNone(directive: CspDirective | undefined): boolean {
  if (!directive) return false;
  return directive.sources.every(
    (source) => source.kind === 'none' || source.kind === 'self',
  );
}

/**
 * Parse and audit one or more CSP headers. The audit runs per policy (each
 * header is independently enforceable) and additionally builds the strictest
 * intersection view for downstream evaluation.
 */
export function auditCsp(
  headers: string | string[],
  options: CspAuditOptions = {},
): CspAuditResult {
  const policies = parsePolicies(headers);
  const findings: CspFinding[] = [];
  const riskyHosts = options.extraRiskyHosts
    ? [...KNOWN_RISKY_HOSTS, ...options.extraRiskyHosts.map((h) => h.toLowerCase())]
    : KNOWN_RISKY_HOSTS;

  const push = (finding: Omit<CspFinding, 'policyIndex'>, policyIndex: number): void => {
    findings.push({ ...finding, policyIndex });
  };

  policies.forEach((policy, policyIndex) => {
    // Duplicate directives inside the same header.
    for (const duplicate of policy.duplicates) {
      push(
        {
          code: 'DUPLICATE_DIRECTIVE',
          severity: 'info',
          message: `Duplicate directive "${duplicate.name}" is ignored; only the first occurrence is enforced (ignored: "${duplicate.ignoredValue}")`,
          directive: duplicate.name,
        },
        policyIndex,
      );
    }

    const effective = resolveScriptSources(policy);
    const scriptDirectiveName = effective.resolvedFrom;

    if (!scriptDirectiveName) {
      push(
        {
          code: 'MISSING_DEFAULT_OR_SCRIPT_SRC',
          severity: 'high',
          message:
            "No script-src, script-src-elem or default-src directive: inline and external scripts are unrestricted",
        },
        policyIndex,
      );
    }

    // `effective.sources` already drops expressions ignored by
    // 'strict-dynamic' per CSP3, so iterating it reports only live bypasses.
    for (const source of effective.sources) {
      switch (source.kind) {
        case 'unsafe-inline':
          push(
            {
              code: 'UNSAFE_INLINE',
              severity: 'high',
              message:
                "'unsafe-inline' permits arbitrary inline scripts (ignored only when nonce/hash or 'strict-dynamic' is present)",
              directive: scriptDirectiveName,
              source: source.raw,
            },
            policyIndex,
          );
          break;
        case 'unsafe-eval':
          push(
            {
              code: 'UNSAFE_EVAL',
              severity: 'high',
              message:
                "'unsafe-eval' permits eval()/new Function/string-to-code execution",
              directive: scriptDirectiveName,
              source: source.raw,
            },
            policyIndex,
          );
          break;
        case 'wildcard':
          push(
            {
              code: 'WILDCARD_SCRIPT',
              severity: 'high',
              message:
                'Wildcard * in script allowlist permits scripts from any host (except data:)',
              directive: scriptDirectiveName,
              source: source.raw,
            },
            policyIndex,
          );
          break;
        case 'scheme':
          if (source.scheme === 'data') {
            push(
              {
                code: 'DATA_SCRIPT',
                severity: 'high',
                message:
                  'data: scheme allows data:text/javascript script execution, equivalent to arbitrary script injection',
                directive: scriptDirectiveName,
                source: source.raw,
              },
              policyIndex,
            );
          } else if (source.scheme === 'http' || source.scheme === 'https') {
            push(
              {
                code: 'BROAD_SCHEME_SCRIPT',
                severity:
                  source.scheme === 'http' ? 'high' : 'medium',
                message:
                  source.scheme === 'http'
                    ? 'http: scheme permits plaintext script loads (MITM script injection)'
                    : 'https: scheme allows scripts from ANY secure origin; consider an explicit host allowlist or strict-dynamic',
                directive: scriptDirectiveName,
                source: source.raw,
              },
              policyIndex,
            );
          }
          break;
        case 'host':
          if (source.host === '*' || source.host === '') {
            push(
              {
                code: 'UNSAFE_HOST_SCRIPT',
                severity: 'high',
                message: `Over-broad host source "${source.raw}" in script directive`,
                directive: scriptDirectiveName,
                source: source.raw,
              },
              policyIndex,
            );
          } else if (isRiskyHost(source.host, riskyHosts)) {
            push(
              {
                code: 'JSONP_OR_REDIRECT_SOURCE',
                severity: 'high',
                message: `Script allowlist host "${source.host}" is a known JSONP / open-redirect sink and can be abused to run arbitrary JS`,
                directive: scriptDirectiveName,
                source: source.raw,
              },
              policyIndex,
            );
          }
          break;
        default:
          break;
      }
    }

    if (
      effective.hasStrictDynamic &&
      !effective.hasNonceOrHash
    ) {
      push(
        {
          code: 'STRICT_DYNAMIC_WITHOUT_NONCE_OR_HASH',
          severity: 'medium',
          message:
            "'strict-dynamic' without a nonce/hash has no trusted root: host allowlists are still ignored, leaving no way to load legitimate scripts",
          directive: scriptDirectiveName,
        },
        policyIndex,
      );
    }

    // object-src: falls back to default-src; 'none' is the safe baseline.
    const objectDirective = resolveDirective(policy, 'object-src');
    const hasExplicitObjectSrc = policy.directives.has('object-src');
    if (!hasExplicitObjectSrc && !objectDirective) {
      push(
        {
          code: 'MISSING_OBJECT_SRC',
          severity: 'medium',
          message:
            "Missing object-src: plugins/embeds are unrestricted; add object-src 'none'",
        },
        policyIndex,
      );
    } else if (hasExplicitObjectSrc && !directiveIsNone(objectDirective)) {
      push(
        {
          code: 'OBJECT_SRC_UNSAFE',
          severity: 'medium',
          message: `object-src is not 'none' (current: ${objectDirective.value}); plugin/embed content remains reachable`,
          directive: 'object-src',
        },
        policyIndex,
      );
    }

    // base-uri has NO default-src fallback and controls <base> injection.
    const baseUri = policy.directives.get('base-uri');
    if (!baseUri) {
      push(
        {
          code: 'MISSING_BASE_URI',
          severity: 'high',
          message:
            "Missing base-uri: an injected <base> tag can hijack all relative script URLs (base-uri does not fall back to default-src)",
        },
        policyIndex,
      );
    } else if (!directiveAllowsOnlySelfOrNone(baseUri)) {
      push(
        {
          code: 'BASE_URI_UNSAFE',
          severity: 'high',
          message: `base-uri should be restricted to 'none' or 'self' (current: ${baseUri.value}); otherwise <base> hijacking remains possible`,
          directive: 'base-uri',
        },
        policyIndex,
      );
    }

    // Wildcards in other fetch directives widen the attack surface.
    for (const [name, directive] of policy.directives) {
      if (
        name !== (scriptDirectiveName ?? '') &&
        directive.sources.some((source) => source.kind === 'wildcard')
      ) {
        push(
          {
            code: 'WILDCARD_SCOPE',
            severity: 'low',
            message: `Wildcard * in "${name}" allows resources from any host`,
            directive: name,
          },
          policyIndex,
        );
      }
    }
  });

  return {
    policies,
    findings,
    merged: intersectPolicies(policies),
  };
}

/** Convenience predicate: true when the audit produced no findings at/above a severity. */
export function hasBlockingFindings(
  result: CspAuditResult,
  threshold: Severity = 'medium',
): boolean {
  const rank: Record<Severity, number> = { info: 0, low: 1, medium: 2, high: 3 };
  return result.findings.some(
    (finding) => rank[finding.severity] >= rank[threshold],
  );
}

// ---------------------------------------------------------------------------
// External URL evaluation
// ---------------------------------------------------------------------------

function hostMatches(source: CspSource, host: string): boolean {
  if (!source.host) return false;
  if (source.hostWildcard) {
    return host.endsWith(`.${source.host}`);
  }
  return host === source.host;
}

/**
 * Evaluate whether an external script URL is allowed by a single policy's
 * resolved (strict-dynamic-aware) script sources.
 */
export function evaluateExternalScriptForPolicy(
  policy: ParsedPolicy,
  url: string,
): { allowed: boolean; reason: string } {
  const effective = resolveScriptSources(policy);
  let target: URL;
  try {
    target = new URL(url);
  } catch {
    return { allowed: false, reason: 'unparseable URL' };
  }

  // With strict-dynamic, classic host/scheme allowlists are ignored; only
  // scripts propagated by a trusted (nonce/hash) parser can execute.
  if (effective.hasStrictDynamic) {
    return {
      allowed: false,
      reason:
        "'strict-dynamic' ignores host/scheme allowlists; only nonce/hash-propagated scripts run",
    };
  }

  for (const source of effective.sources) {
    switch (source.kind) {
      case 'wildcard':
        return { allowed: true, reason: 'wildcard *' };
      case 'self':
        // Caller-side self origin is not supplied; treat 'self' as same-origin
        // placeholder and expose the matched keyword.
        return { allowed: true, reason: "'self'" };
      case 'scheme':
        if (source.scheme === target.protocol.replace(':', '').toLowerCase()) {
          return { allowed: true, reason: `scheme ${source.scheme}:` };
        }
        break;
      case 'host':
        if (
          hostMatches(source, target.hostname.toLowerCase()) &&
          (!source.scheme ||
            source.scheme === target.protocol.replace(':', '').toLowerCase())
        ) {
          return { allowed: true, reason: `host ${source.raw}` };
        }
        break;
      case 'none':
        return { allowed: false, reason: "'none' blocks all scripts" };
      default:
        break;
    }
  }

  return {
    allowed: false,
    reason: `no matching source (resolved via ${effective.resolvedFrom ?? 'none'})`,
  };
}

/** Evaluate an external script URL against every enforced policy (AND logic). */
export function evaluateExternalScript(
  policies: ParsedPolicy[],
  url: string,
): { allowed: boolean; reasons: { policyIndex: number; allowed: boolean; reason: string }[] } {
  const reasons = policies.map((policy, policyIndex) => {
    const result = evaluateExternalScriptForPolicy(policy, url);
    return { policyIndex, allowed: result.allowed, reason: result.reason };
  });
  return { allowed: reasons.every((reason) => reason.allowed), reasons };
}
