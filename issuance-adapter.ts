import {
  PrivateStateTokenService,
  type PrivateStateToken,
} from "./private-state-token.ts";

export interface IssuanceAdapterRequest {
  readonly origin: string;
  readonly ttlMs?: number;
}

/**
 * Thin adapter used by the issuance flow. It delegates to the importable
 * private-state-token library rather than implementing origin binding itself.
 */
export class IssuanceAdapter {
  private readonly tokens: PrivateStateTokenService;

  constructor(tokens: PrivateStateTokenService) {
    this.tokens = tokens;
  }

  issue(request: IssuanceAdapterRequest | string): PrivateStateToken {
    const normalizedRequest =
      typeof request === "string" ? { origin: request } : request;

    return this.tokens.issue(normalizedRequest);
  }

  issueToken(origin: string, ttlMs?: number): PrivateStateToken {
    return this.issue(
      ttlMs === undefined ? { origin } : { origin, ttlMs },
    );
  }
}

export function createIssuanceAdapter(
  tokens: PrivateStateTokenService,
): IssuanceAdapter {
  return new IssuanceAdapter(tokens);
}
