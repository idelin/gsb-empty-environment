import {
  isSensitiveParamName,
  stripSensitiveParams,
  type SensitiveParamOptions,
} from "./params.ts";
import {
  getRequestTarget,
  isSensitivePath,
  type SensitivePathOptions,
} from "./paths.ts";
import {
  DEFAULT_REFERRER_POLICY,
  SENSITIVE_REFERRER_POLICY,
  type ReferrerPolicyValue,
} from "./policy.ts";
import { isOriginAllowed } from "./origins.ts";

export type SensitiveExternalMode = "block" | "relay" | "sanitize";

export interface RelayLinkInfo {
  originalTarget: string;
  safeTarget: string;
  sensitivePage: boolean;
}

export type RelayLinkFactory = (
  target: string,
  info: RelayLinkInfo,
) => string | { href: string; token?: string } | null | undefined;

export interface LinkContext {
  pagePath?: string;
  pageUrl?: string;
  baseUrl?: string;
  currentOrigin?: string;
  sensitive?: boolean;
  mode?: SensitiveExternalMode;
}

export interface OutboundLinkOptions
  extends SensitivePathOptions,
    LinkContext {
  /** How to handle external links from sensitive pages. */
  mode?: SensitiveExternalMode;
  /** One-time relay factory. Required for `mode: "relay"`. */
  relay?: RelayLinkFactory;
  /** Simple relay prefix or function used when no stateful relay is wired. */
  relayUrl?: string | ((target: string) => string);
  /** Permit direct (sanitized) external links from sensitive pages. */
  allowExternalOnSensitive?: boolean;
  /** Permit any external link. Defaults to true for non-sensitive pages. */
  allowExternal?: boolean;
  /** Protocols allowed in outbound links. */
  allowProtocols?: readonly string[];
  /** Optional origin allow-list for external destinations. */
  allowedOrigins?: readonly string[];
  /** Remove URL credentials from the emitted href. Defaults to true. */
  stripCredentials?: boolean;
}

export type OutboundLinkKind =
  | "internal"
  | "external"
  | "external-relay"
  | "blocked"
  | "unsafe";

export interface OutboundLinkDecision {
  href: string | null;
  kind: OutboundLinkKind;
  external: boolean;
  sensitivePage: boolean;
  blocked: boolean;
  reason?: string;
  originalTarget: string;
  safeTarget: string | null;
  rel: string;
  referrerPolicy: ReferrerPolicyValue;
  relayed: boolean;
  relayToken?: string;
}

const DEFAULT_PROTOCOLS = ["http:", "https:", "mailto:", "tel:"] as const;
const UNSAFE_SCHEMES = ["javascript:", "data:", "vbscript:", "file:", "blob:"];
const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f]/g;

function cleanTarget(raw: string): string {
  return raw.replace(CONTROL_CHARACTERS, "").trim();
}

function hasScheme(value: string): boolean {
  return /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(value);
}

function parseTarget(
  target: string,
  base: string,
): URL | null {
  const raw = cleanTarget(target);
  if (raw.length === 0) return null;
  try {
    if (raw.startsWith("//")) return new URL(raw, base);
    if (hasScheme(raw)) return new URL(raw);
    return new URL(raw, base);
  } catch {
    return null;
  }
}

function normalizeBase(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length === 0) return "https://portal.invalid";
  if (trimmed.startsWith("//")) return `https:${trimmed}`;
  if (hasScheme(trimmed)) return trimmed;
  if (trimmed.startsWith("/")) return `https://portal.invalid${trimmed}`;
  return `https://${trimmed}`;
}

function originOf(value: string | undefined): string {
  if (value === undefined || value.length === 0) {
    return "https://portal.invalid";
  }
  try {
    return new URL(normalizeBase(value)).origin;
  } catch {
    return "https://portal.invalid";
  }
}

function normalizeProtocol(protocol: string): string {
  const normalized = protocol.toLowerCase();
  return normalized.endsWith(":") ? normalized : `${normalized}:`;
}

/** Returns true when `target` resolves to a different origin than `base`. */
export function isExternalUrl(
  target: string | URL,
  base?: string | URL,
): boolean {
  const baseString =
    base instanceof URL
      ? base.href
      : normalizeBase(base ?? "https://portal.invalid");
  const raw =
    target instanceof URL ? target.href : cleanTarget(String(target));

  if (raw.length === 0) return false;
  if (!hasScheme(raw) && !raw.startsWith("//")) return false;

  const parsed = parseTarget(raw, baseString);
  if (parsed === null) return true;

  const protocol = normalizeProtocol(parsed.protocol);
  if (protocol !== "http:" && protocol !== "https:") return true;

  return parsed.origin !== originOf(baseString);
}

/** True for schemes that must never be rendered as a link. */
export function isUnsafeUrl(target: string | URL): boolean {
  const raw =
    target instanceof URL
      ? target.href
      : cleanTarget(String(target)).toLowerCase();
  if (raw.length === 0) return true;
  return UNSAFE_SCHEMES.some((scheme) => raw.startsWith(scheme));
}

function protocolAllowed(
  protocol: string,
  allowProtocols: readonly string[],
): boolean {
  const normalized = normalizeProtocol(protocol);
  return allowProtocols.some(
    (allowed) => normalizeProtocol(String(allowed)) === normalized,
  );
}

function mergeOptions(
  defaults: OutboundLinkOptions,
  context: LinkContext | undefined,
): OutboundLinkOptions {
  if (context === undefined) return defaults;
  return { ...defaults, ...context };
}

/**
 * Classifies and sanitizes one outbound link.
 *
 * External links are always stripped of token-like query/fragment parameters.
 * On sensitive pages they are blocked or rewritten through a one-time relay;
 * direct external links are only possible with an explicit opt-in.
 */
export function classifyOutboundLink(
  target: string | URL,
  options: OutboundLinkOptions = {},
): OutboundLinkDecision {
  const rawTarget = target instanceof URL ? target.href : String(target);
  const cleaned = cleanTarget(rawTarget);
  const baseUrl = normalizeBase(
    options.baseUrl ?? options.currentOrigin ?? "https://portal.invalid",
  );
  const pageTarget =
    options.pageUrl ?? options.pagePath ?? getRequestTarget(baseUrl);
  const sensitivePage =
    options.sensitive ?? isSensitivePath(pageTarget, options);
  const sensitivePolicy: ReferrerPolicyValue = sensitivePage
    ? SENSITIVE_REFERRER_POLICY
    : DEFAULT_REFERRER_POLICY;

  const blocked = (
    kind: "blocked" | "unsafe",
    reason: string,
    external = false,
  ): OutboundLinkDecision => ({
    href: null,
    kind,
    external,
    sensitivePage,
    blocked: true,
    reason,
    originalTarget: rawTarget,
    safeTarget: null,
    rel: "noopener noreferrer",
    referrerPolicy: sensitivePolicy,
    relayed: false,
  });

  if (cleaned.length === 0) return blocked("blocked", "empty-target");
  if (isUnsafeUrl(cleaned)) return blocked("unsafe", "unsafe-scheme");

  const parsed = parseTarget(cleaned, baseUrl);
  if (parsed === null) return blocked("blocked", "invalid-url");

  const protocols = options.allowProtocols ?? DEFAULT_PROTOCOLS;
  if (!protocolAllowed(parsed.protocol, protocols)) {
    return blocked("unsafe", "disallowed-protocol");
  }

  const external = isExternalUrl(parsed, baseUrl);
  if (
    external &&
    options.allowedOrigins !== undefined &&
    !isOriginAllowed(parsed.origin, options.allowedOrigins)
  ) {
    return blocked("blocked", "external-origin-not-allowed", true);
  }
  const safeTarget = stripSensitiveParams(cleaned, {
    ...options,
    stripCredentials: options.stripCredentials !== false,
  });

  if (!external) {
    if (safeTarget.length === 0) {
      return blocked("blocked", "empty-after-sanitization");
    }
    return {
      href: safeTarget,
      kind: "internal",
      external: false,
      sensitivePage,
      blocked: false,
      originalTarget: rawTarget,
      safeTarget,
      rel: "noopener noreferrer",
      referrerPolicy: sensitivePolicy,
      relayed: false,
    };
  }

  if (options.allowExternal === false) {
    return blocked("blocked", "external-not-allowed", true);
  }

  if (sensitivePage) {
    const directAllowed =
      options.allowExternalOnSensitive === true ||
      options.mode === "sanitize";
    if (directAllowed) {
      return {
        href: safeTarget,
        kind: "external",
        external: true,
        sensitivePage,
        blocked: false,
        originalTarget: rawTarget,
        safeTarget,
        rel: "noopener noreferrer",
        referrerPolicy: "no-referrer",
        relayed: false,
      };
    }

    const mode =
      options.mode ?? (options.relay !== undefined ? "relay" : "block");
    if (mode === "block") {
      return blocked("blocked", "external-blocked-on-sensitive-page", true);
    }

    if (mode === "relay") {
      const info: RelayLinkInfo = {
        originalTarget: rawTarget,
        safeTarget,
        sensitivePage: true,
      };

      if (options.relay !== undefined) {
        const relayed = options.relay(safeTarget, info);
        if (relayed === null || relayed === undefined) {
          return blocked("blocked", "relay-rejected", true);
        }
        const href = typeof relayed === "string" ? relayed : relayed.href;
        const token =
          typeof relayed === "string" ? undefined : relayed.token;
        if (href.length === 0) return blocked("blocked", "relay-rejected", true);
        if (isUnsafeUrl(href)) {
          return blocked("unsafe", "unsafe-relay-href", true);
        }
        return {
          href,
          kind: "external-relay",
          external: true,
          sensitivePage,
          blocked: false,
          originalTarget: rawTarget,
          safeTarget,
          rel: "noopener noreferrer",
          referrerPolicy: "no-referrer",
          relayed: true,
          relayToken: token,
        };
      }

      if (options.relayUrl !== undefined) {
        const href =
          typeof options.relayUrl === "function"
            ? options.relayUrl(safeTarget)
            : options.relayUrl
                .replace(/\{url\}|\{target\}/g, encodeURIComponent(safeTarget));
        if (href.length === 0) return blocked("blocked", "relay-rejected", true);
        if (isUnsafeUrl(href)) {
          return blocked("unsafe", "unsafe-relay-href", true);
        }
        return {
          href,
          kind: "external-relay",
          external: true,
          sensitivePage,
          blocked: false,
          originalTarget: rawTarget,
          safeTarget,
          rel: "noopener noreferrer",
          referrerPolicy: "no-referrer",
          relayed: true,
        };
      }

      return blocked("blocked", "relay-unavailable", true);
    }
  }

  return {
    href: safeTarget,
    kind: "external",
    external: true,
    sensitivePage,
    blocked: false,
    originalTarget: rawTarget,
    safeTarget,
    rel: "noopener noreferrer",
    referrerPolicy: sensitivePolicy,
    relayed: false,
  };
}

/** Creates a link classifier with portal-wide defaults. */
export function createOutboundLinkGuard(
  defaults: OutboundLinkOptions = {},
): (target: string | URL, context?: LinkContext) => OutboundLinkDecision {
  return (target, context) =>
    classifyOutboundLink(target, mergeOptions(defaults, context));
}

export const createLinkGuard = createOutboundLinkGuard;

/** Returns a safe href, or null when the link must not be rendered. */
export function createSafeLink(
  target: string | URL,
  options: OutboundLinkOptions = {},
): string | null {
  return classifyOutboundLink(target, options).href;
}

/** Alias used by view helpers. */
export const getSafeLink = createSafeLink;
export const getSafeLinkHref = createSafeLink;
export const safeExternalLink = createSafeLink;
export const buildSafeExternalLink = createSafeLink;

/** Alias for the structured classifier. */
export const createOutboundLink = classifyOutboundLink;

export interface SafeAnchorProps {
  href?: string;
  rel: string;
  referrerPolicy: ReferrerPolicyValue;
  target?: string;
  "aria-disabled"?: "true";
  [key: string]: string | undefined;
}

export interface SafeAnchorOptions extends OutboundLinkOptions {
  /** Optional anchor target such as `_blank`. */
  anchorTarget?: string;
}

/** Produces safe props for a plain `<a>` or a framework link component. */
export function createSafeAnchorProps(
  target: string | URL,
  options: SafeAnchorOptions = {},
): SafeAnchorProps {
  const decision = classifyOutboundLink(target, options);
  if (decision.blocked || decision.href === null) {
    return {
      rel: "noopener noreferrer",
      referrerPolicy: "no-referrer",
      "aria-disabled": "true",
    };
  }
  const props: SafeAnchorProps = {
    href: decision.href,
    rel: decision.rel,
    referrerPolicy: decision.referrerPolicy,
  };
  if (options.anchorTarget !== undefined) props.target = options.anchorTarget;
  return props;
}

/** Alias for view-layer usage. */
export const safeAnchorProps = createSafeAnchorProps;

/** Sanitizes a URL that is about to be placed in an external link. */
export function sanitizeOutboundUrl(
  target: string | URL,
  options: SensitiveParamOptions = {},
): string {
  return stripSensitiveParams(target, {
    ...options,
    stripCredentials: options.stripCredentials !== false,
  });
}

/** True if a parameter name would be removed by the link sanitizer. */
export function isBlockedLinkParam(name: string): boolean {
  return isSensitiveParamName(name);
}
