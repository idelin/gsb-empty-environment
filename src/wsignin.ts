import {
  resolveAudience,
  validateWreply,
  WreplyValidationError,
  type RelyingParty,
} from "./reply-url.ts";

export interface WsigninRequest {
  wa?: string;
  wtrealm?: string;
  wreply?: string;
  wctx?: string;
}

export interface SigninDecision {
  /** Token audience: always the registered realm value. */
  audience: string;
  /** Validated reply URL the token may be posted to. */
  replyTo: string;
  wctx?: string;
}

export class WsigninRequestError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WsigninRequestError";
  }
}

/**
 * Handle a WS-Federation wsignin1.0 request: look up the relying party by
 * wtrealm, validate wreply against its registered reply addresses, and
 * decide the token audience and reply target.
 */
export function handleWsignin(
  request: WsigninRequest,
  registry: RelyingParty[],
): SigninDecision {
  if (request.wa !== "wsignin1.0") {
    throw new WsigninRequestError(`unsupported wa value '${request.wa}'`);
  }
  if (!request.wtrealm) {
    throw new WsigninRequestError("wtrealm is required");
  }
  const party = registry.find((entry) => entry.realm === request.wtrealm);
  if (!party) {
    throw new WsigninRequestError("wtrealm is not a registered relying party");
  }

  let replyTo: string;
  if (request.wreply !== undefined) {
    replyTo = validateWreply(request.wreply, party).toString();
  } else {
    const fallback = party.replyAddresses[0];
    if (fallback === undefined) {
      throw new WreplyValidationError(
        "relying party has no registered reply address",
      );
    }
    replyTo = fallback;
  }

  return {
    audience: resolveAudience(party),
    replyTo,
    ...(request.wctx !== undefined ? { wctx: request.wctx } : {}),
  };
}
