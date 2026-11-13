import type {
  BlockedRequest,
  MixedContentDecision,
  MixedContentRequest,
} from "./types.ts";
import {
  inferSecureContext,
  isActiveKind,
  isPlaintextScheme,
  isSecureScheme,
  isUpgradeableKind,
  resolveUrl,
  upgradeToSecure,
} from "./url.ts";

function block(
  request: MixedContentRequest,
  originalUrl: string,
  reason: BlockedRequest["reason"],
  upgradedUrl?: URL,
): BlockedRequest {
  return {
    allowed: false,
    kind: request.kind,
    reason,
    originalUrl,
    ...(upgradedUrl === undefined ? {} : { upgradedUrl }),
  };
}

/**
 * Decide whether a request may be issued from a document.
 *
 * Rules:
 *  - Non-secure documents are not subject to mixed-content blocking.
 *  - Secure documents requesting secure URLs are always allowed.
 *  - Active mixed content (script, XHR/fetch, module, WebSocket) over
 *    http/ws is blocked; there is no upgrade fallback.
 *  - Passive content (image and friends) over http is upgraded to https.
 *    The returned decision marks `upgraded: true`; whether the upgraded URL
 *    is reachable is resolved by `assembleRequest`, which never falls back
 *    to the plaintext URL.
 *  - Non-http special schemes (data:, blob:, file:) pass through.
 */
export function decideMixedContent(
  request: MixedContentRequest,
): MixedContentDecision {
  let url: URL;
  try {
    url = resolveUrl(request.resourceUrl, request.documentUrl);
  } catch {
    return block(
      request,
      String(request.resourceUrl),
      "invalid-url",
    );
  }

  const secureContext =
    request.isSecureContext ?? inferSecureContext(request.documentUrl);

  if (!secureContext) {
    return { allowed: true, url, kind: request.kind, upgraded: false };
  }

  if (isSecureScheme(url)) {
    return { allowed: true, url, kind: request.kind, upgraded: false };
  }

  if (!isPlaintextScheme(url)) {
    // Unknown/other special schemes: not mixed content, let the network layer
    // apply its own scheme policy.
    return { allowed: true, url, kind: request.kind, upgraded: false };
  }

  if (isActiveKind(request.kind)) {
    return block(request, url.href, "active-mixed-content");
  }

  if (isUpgradeableKind(request.kind)) {
    const upgraded = upgradeToSecure(url);
    return { allowed: true, url: upgraded, kind: request.kind, upgraded: true };
  }

  // Unknown plaintext resource kinds default to the conservative path: block.
  return block(request, url.href, "active-mixed-content");
}

/** Type guard: true when the decision forbids sending the request. */
export function isBlocked(
  decision: MixedContentDecision,
): decision is BlockedRequest {
  return decision.allowed === false;
}
