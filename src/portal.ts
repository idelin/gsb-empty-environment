import {
  classifyOutboundLink,
  createSafeAnchorProps,
  type LinkContext,
  type OutboundLinkDecision,
  type OutboundLinkOptions,
  type RelayLinkInfo,
  type SafeAnchorOptions,
  type SafeAnchorProps,
  type SensitiveExternalMode,
} from "./links.ts";
import { buildShareableUrl, type ShareableUrlOptions } from "./params.ts";
import { getPathname, getRequestTarget } from "./paths.ts";
import {
  applyReferrerPolicy,
  buildReferrerPolicyHeaders,
  createReferrerPolicyMiddleware,
  ReferrerPolicyGrader,
  resolveReferrerPolicy,
  type ReferrerPolicyHeaders,
  type ReferrerPolicyOptions,
  type ReferrerPolicyValue,
} from "./policy.ts";
import {
  createRedirectHandler,
  handleRedirectRequest,
  isRedirectPath,
  type RedirectExternalMode,
  type RedirectHandlerOptions,
  type RedirectResult,
} from "./redirect.ts";
import {
  createRelayHandler,
  isRelayPath,
  OneTimeRelay,
  type OneTimeRelayOptions,
  type RelayResponse,
} from "./relay.ts";

export interface PortalReferrerOptions extends ReferrerPolicyOptions {
  baseUrl?: string;
  /** Reuse an existing relay, create one, or disable relaying. */
  relay?: OneTimeRelay | OneTimeRelayOptions | false;
  relayPath?: string;
  redirectPath?: string;
  sensitiveExternalMode?: SensitiveExternalMode;
  redirectExternalMode?: RedirectExternalMode;
  allowExternalOnSensitive?: boolean;
  allowAnyExternal?: boolean;
  allowedOrigins?: readonly string[];
  allowedProtocols?: readonly string[];
  maxTargetLength?: number;
  redirectTargetParams?: readonly string[];
  stripAllQuery?: boolean;
  relayTtlMs?: number;
  relayMaxEntries?: number;
}

export interface ReferrerPortal {
  options: PortalReferrerOptions;
  grader: ReferrerPolicyGrader;
  relay?: OneTimeRelay;
  policyFor(input?: unknown): ReferrerPolicyValue;
  isSensitive(input?: unknown): boolean;
  headersFor(input?: unknown): ReferrerPolicyHeaders;
  apply(response: unknown, request?: unknown): unknown;
  middleware(
    req: unknown,
    res: unknown,
    next?: () => void,
  ): unknown;
  resolveLink(
    target: string | URL,
    context?: LinkContext,
  ): OutboundLinkDecision;
  createLink(target: string | URL, context?: LinkContext): string | null;
  anchorProps(
    target: string | URL,
    options?: SafeAnchorOptions,
  ): SafeAnchorProps;
  shareUrl(
    input: string | URL,
    params?:
      | Record<string, unknown>
      | URLSearchParams
      | Iterable<readonly [string, string]>,
    options?: ShareableUrlOptions,
  ): string;
  redirect(input: unknown): RedirectResult;
  relayResponse(input: unknown): RelayResponse | null;
  handle(req: unknown, res: unknown): boolean;
}

function makeRelay(
  options: PortalReferrerOptions,
): OneTimeRelay | undefined {
  if (options.relay === false) return undefined;
  if (options.relay instanceof OneTimeRelay) return options.relay;

  const relayOptions: OneTimeRelayOptions = {
    ...(options.relay ?? {}),
    mountPath:
      options.relayPath ??
      options.relay?.mountPath ??
      "/__referrer-relay",
    baseUrl: options.relay?.baseUrl ?? options.baseUrl,
    ttlMs: options.relay?.ttlMs ?? options.relayTtlMs,
    maxEntries: options.relay?.maxEntries ?? options.relayMaxEntries,
    allowedOrigins: options.relay?.allowedOrigins ?? options.allowedOrigins,
    allowedProtocols:
      options.relay?.allowedProtocols ?? options.allowedProtocols,
    maxTargetLength:
      options.relay?.maxTargetLength ?? options.maxTargetLength,
  };
  return new OneTimeRelay(relayOptions);
}

function linkOptions(
  options: PortalReferrerOptions,
  relay: OneTimeRelay | undefined,
  context: LinkContext | undefined,
): OutboundLinkOptions {
  const mode = options.sensitiveExternalMode ?? (relay ? "relay" : "block");
  const relayFactory =
    relay !== undefined && mode === "relay"
      ? (target: string, info: RelayLinkInfo) => {
          // Pass the original target so the relay can see (and remove) any
          // token that the link sanitizer already stripped.
          const link = relay.createLink(info.originalTarget);
          return link === null
            ? null
            : { href: link.url, token: link.token };
        }
      : undefined;

  const merged = { ...options, ...(context ?? {}) } as OutboundLinkOptions;
  return {
    ...merged,
    baseUrl: merged.baseUrl ?? options.baseUrl,
    mode: context?.mode ?? mode,
    relay: relayFactory,
    allowExternalOnSensitive:
      merged.allowExternalOnSensitive ?? options.allowExternalOnSensitive,
  };
}

/**
 * Creates the importable portal component. It provides:
 * - a global/path-graded Referrer-Policy middleware;
 * - token-safe outbound links and share URLs;
 * - a one-time external relay;
 * - a server-side redirect endpoint that strips sensitive query parameters.
 */
export function createReferrerPortal(
  options: PortalReferrerOptions = {},
): ReferrerPortal {
  const relay = makeRelay(options);
  const grader = new ReferrerPolicyGrader(options);
  const relayPath = relay?.mountPath ?? options.relayPath ?? "/__referrer-relay";
  const redirectPath = options.redirectPath;

  const redirectOptions: RedirectHandlerOptions = {
    ...options,
    mountPath: redirectPath,
    relay,
    externalMode: options.redirectExternalMode ?? "direct",
    targetParams:
      options.redirectTargetParams ??
      (options as RedirectHandlerOptions).targetParams,
  };

  const globalPolicyMiddleware = createReferrerPolicyMiddleware(options);
  const relayHandler = relay !== undefined ? createRelayHandler(relay) : undefined;
  const redirectHandler = createRedirectHandler(redirectOptions);

  const portal: ReferrerPortal = {
    options,
    grader,
    relay,

    policyFor(input = "/") {
      return resolveReferrerPolicy(input, options);
    },

    isSensitive(input = "/") {
      return grader.isSensitive(input);
    },

    headersFor(input = "/") {
      return buildReferrerPolicyHeaders(input, options);
    },

    apply(response, request = "/") {
      return applyReferrerPolicy(response, request, options);
    },

    middleware(req, res, next) {
      const target = getRequestTarget(req);
      if (relay !== undefined && isRelayPath(target, relayPath)) {
        return relayHandler?.(req, res, next);
      }
      if (isRedirectPath(target, redirectPath)) {
        return redirectHandler(req, res, next);
      }
      return globalPolicyMiddleware(req, res, next);
    },

    resolveLink(target, context) {
      return classifyOutboundLink(
        target,
        linkOptions(options, relay, context),
      );
    },

    createLink(target, context) {
      return portal.resolveLink(target, context).href;
    },

    anchorProps(target, anchorOptions = {}) {
      return createSafeAnchorProps(
        target,
        {
          ...linkOptions(options, relay, anchorOptions),
          ...anchorOptions,
        },
      );
    },

    shareUrl(input, params, shareOptions = {}) {
      return buildShareableUrl(input, params, shareOptions);
    },

    redirect(input) {
      return handleRedirectRequest(input, redirectOptions);
    },

    relayResponse(input) {
      return relay?.handleRequest(input) ?? null;
    },

    handle(req, res) {
      const target = getRequestTarget(req);
      if (relay !== undefined && isRelayPath(target, relayPath)) {
        relayHandler?.(req, res);
        return true;
      }
      if (isRedirectPath(target, redirectPath)) {
        redirectHandler(req, res);
        return true;
      }
      applyReferrerPolicy(res, req, options);
      return false;
    },
  };

  return portal;
}

/** Aliases for portals that prefer a noun. */
export const createPortalReferrerPolicy = createReferrerPortal;
export const createReferrerPolicyPortal = createReferrerPortal;

/** Convenience wrapper for a standalone middleware. */
export function createPortalMiddleware(
  options: PortalReferrerOptions = {},
): (req: unknown, res: unknown, next?: () => void) => unknown {
  return createReferrerPortal(options).middleware;
}

/** Returns the effective pathname used by the portal's route matchers. */
export function portalPath(input: unknown): string {
  return getPathname(input);
}
