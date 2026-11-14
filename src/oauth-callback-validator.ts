import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import {
  inspectRedirectUri,
  matchRedirectUri,
  parseRedirectUri,
  RedirectUriError,
  type ParsedRedirectUri,
  type RedirectUriPolicy,
} from "./redirect-uri.ts";

export type PkceMethod = "S256" | "plain";

export type OAuthCallbackErrorCode =
  | "invalid_client"
  | "invalid_redirect_uri"
  | "invalid_request"
  | "invalid_grant";

export class OAuthCallbackError extends Error {
  readonly code: OAuthCallbackErrorCode;

  constructor(code: OAuthCallbackErrorCode, message: string) {
    super(message);
    this.name = "OAuthCallbackError";
    this.code = code;
  }
}

export interface ClientRedirectRegistration extends RedirectUriPolicy {
  clientId: string;
  redirectUris: readonly string[];
}

export interface RegisteredClient {
  readonly clientId: string;
  readonly redirectUris: readonly ParsedRedirectUri[];
  readonly policy: RedirectUriPolicy;
}

export interface ResolvedRedirectUri {
  readonly clientId: string;
  /** The address that may receive the authorization response. */
  readonly redirectUri: string;
  readonly wildcard: boolean;
}

export interface AuthorizeInput {
  clientId: string;
  redirectUri: string;
  codeChallenge: string;
  codeChallengeMethod?: PkceMethod;
  nonce?: string;
  state?: string;
  ttlMs?: number;
}

export interface IssuedAuthorizationCode {
  readonly code: string;
  readonly clientId: string;
  /** Canonical registered URI; use this value for the authorization response. */
  readonly redirectUri: string;
  readonly state?: string;
  readonly expiresAt: number;
}

export interface ExchangeAuthorizationCodeInput {
  code: string;
  clientId: string;
  redirectUri: string;
  codeVerifier: string;
  nonce?: string;
}

export interface AuthorizationCodeGrant {
  readonly clientId: string;
  readonly redirectUri: string;
  readonly codeChallenge: string;
  readonly codeChallengeMethod: PkceMethod;
  readonly nonce?: string;
}

interface StoredAuthorizationCode extends AuthorizationCodeGrant {
  readonly expiresAt: number;
}

export interface OAuthCallbackValidatorOptions extends RedirectUriPolicy {
  now?: () => number;
  defaultCodeTtlMs?: number;
  clients?: readonly ClientRedirectRegistration[];
}

const DEFAULT_CODE_TTL_MS = 5 * 60 * 1000;
export function createPkceChallenge(
  codeVerifier: string,
  method: PkceMethod = "S256",
): string {
  if (method === "plain") {
    return codeVerifier;
  }
  return createHash("sha256").update(codeVerifier, "utf8").digest("base64url");
}

export function verifyPkce(
  codeVerifier: string,
  codeChallenge: string,
  method: PkceMethod = "S256",
): boolean {
  if (typeof codeVerifier !== "string" || typeof codeChallenge !== "string") {
    return false;
  }
  const expected = createPkceChallenge(codeVerifier, method);
  const expectedBuffer = Buffer.from(expected, "utf8");
  const challengeBuffer = Buffer.from(codeChallenge, "utf8");
  if (expectedBuffer.length !== challengeBuffer.length) {
    return false;
  }
  return timingSafeEqual(expectedBuffer, challengeBuffer);
}

function fail(code: OAuthCallbackErrorCode, message: string): never {
  throw new OAuthCallbackError(code, message);
}

function normalizeClientId(clientId: string): string {
  if (typeof clientId !== "string" || clientId.length === 0) {
    fail("invalid_client", "A non-empty client identifier is required.");
  }
  return clientId;
}

/**
 * Redirect URI registry plus authorization-code binding for a TypeScript
 * authorization server. Authorization codes are only issued after the
 * requested redirect URI matches a registered URI or path template.
 */
export class OAuthCallbackValidator {
  private readonly clients = new Map<string, RegisteredClient>();
  private readonly authorizationCodes = new Map<string, StoredAuthorizationCode>();
  private readonly now: () => number;
  private readonly defaultCodeTtlMs: number;
  private readonly defaultPolicy: RedirectUriPolicy;

  constructor(options: OAuthCallbackValidatorOptions = {}) {
    this.now = options.now ?? Date.now;
    this.defaultCodeTtlMs = options.defaultCodeTtlMs ?? DEFAULT_CODE_TTL_MS;
    this.defaultPolicy = {
      allowLoopback: options.allowLoopback,
      allowCustomSchemes: options.allowCustomSchemes,
      allowCustomScheme: options.allowCustomScheme,
    };
    for (const client of options.clients ?? []) {
      this.registerClient(client);
    }
  }

  registerClient(
    clientId: string,
    redirectUris: readonly string[],
    policy?: RedirectUriPolicy,
  ): RegisteredClient;
  registerClient(registration: ClientRedirectRegistration): RegisteredClient;
  registerClient(
    clientIdOrRegistration: string | ClientRedirectRegistration,
    redirectUris: readonly string[] = [],
    policy: RedirectUriPolicy = {},
  ): RegisteredClient {
    const registration: ClientRedirectRegistration =
      typeof clientIdOrRegistration === "string"
        ? {
            clientId: clientIdOrRegistration,
            redirectUris,
            ...policy,
          }
        : clientIdOrRegistration;

    const clientId = normalizeClientId(registration.clientId);
    const effectivePolicy: RedirectUriPolicy = {
      allowLoopback: registration.allowLoopback ?? this.defaultPolicy.allowLoopback,
      allowCustomSchemes:
        registration.allowCustomSchemes ?? this.defaultPolicy.allowCustomSchemes,
      allowCustomScheme:
        registration.allowCustomScheme ?? this.defaultPolicy.allowCustomScheme,
    };
    const parsed: ParsedRedirectUri[] = [];
    const seen = new Set<string>();

    for (const redirectUri of registration.redirectUris) {
      let candidate: ParsedRedirectUri;
      try {
        candidate = parseRedirectUri(redirectUri, effectivePolicy);
      } catch (error) {
        if (error instanceof RedirectUriError) {
          fail("invalid_redirect_uri", error.message);
        }
        throw error;
      }
      if (seen.has(candidate.uri)) {
        continue;
      }
      seen.add(candidate.uri);
      parsed.push(candidate);
    }

    if (parsed.length === 0) {
      fail("invalid_redirect_uri", "A client must register at least one redirect URI.");
    }

    const registered: RegisteredClient = {
      clientId,
      redirectUris: parsed,
      policy: effectivePolicy,
    };
    this.clients.set(clientId, registered);
    return registered;
  }

  getClient(clientId: string): RegisteredClient | undefined {
    return this.clients.get(clientId);
  }

  getRegisteredRedirectUris(clientId: string): readonly string[] {
    const client = this.requireClient(clientId);
    return client.redirectUris.map((uri) => uri.uri);
  }

  resolveRedirectUri(clientId: string, requestedRedirectUri: string): ResolvedRedirectUri {
    const client = this.requireClient(clientId);

    let requested: ParsedRedirectUri;
    try {
      requested = inspectRedirectUri(requestedRedirectUri);
    } catch (error) {
      if (error instanceof RedirectUriError) {
        fail("invalid_redirect_uri", error.message);
      }
      throw error;
    }

    let wildcardMatch: ParsedRedirectUri | undefined;
    for (const registered of client.redirectUris) {
      if (!redirectUriPartsMatch(registered, requested)) {
        continue;
      }
      if (!registered.hasWildcard) {
        return {
          clientId: client.clientId,
          redirectUri: registered.uri,
          wildcard: false,
        };
      }
      wildcardMatch ??= registered;
    }

    if (wildcardMatch) {
      return {
        clientId: client.clientId,
        redirectUri: requested.uri,
        wildcard: true,
      };
    }

    fail(
      "invalid_redirect_uri",
      "The redirect URI does not match any URI registered for this client.",
    );
  }

  isRedirectUriAllowed(clientId: string, requestedRedirectUri: string): boolean {
    try {
      this.resolveRedirectUri(clientId, requestedRedirectUri);
      return true;
    } catch {
      return false;
    }
  }

  authorize(input: AuthorizeInput): IssuedAuthorizationCode {
    const clientId = normalizeClientId(input.clientId);
    const resolved = this.resolveRedirectUri(clientId, input.redirectUri);
    const method = input.codeChallengeMethod ?? "S256";

    if (method !== "S256" && method !== "plain") {
      fail("invalid_request", "Unsupported PKCE challenge method.");
    }
    if (
      typeof input.codeChallenge !== "string" ||
      input.codeChallenge.length === 0 ||
      input.codeChallenge.length > 256 ||
      /[\u0000-\u001f\u007f]/.test(input.codeChallenge)
    ) {
      fail("invalid_request", "A valid PKCE code_challenge is required.");
    }
    if (input.nonce !== undefined && typeof input.nonce !== "string") {
      fail("invalid_request", "The nonce must be a string.");
    }

    const ttlMs = input.ttlMs ?? this.defaultCodeTtlMs;
    if (!Number.isFinite(ttlMs) || ttlMs <= 0) {
      fail("invalid_request", "The authorization code lifetime is invalid.");
    }

    const code = randomBytes(32).toString("base64url");
    const record: StoredAuthorizationCode = {
      clientId,
      redirectUri: resolved.redirectUri,
      codeChallenge: input.codeChallenge,
      codeChallengeMethod: method,
      nonce: input.nonce,
      expiresAt: this.now() + ttlMs,
    };
    this.authorizationCodes.set(code, record);

    const issued: IssuedAuthorizationCode = {
      code,
      clientId,
      redirectUri: resolved.redirectUri,
      expiresAt: record.expiresAt,
    };
    if (input.state !== undefined) {
      return { ...issued, state: input.state };
    }
    return issued;
  }

  exchange(input: ExchangeAuthorizationCodeInput): AuthorizationCodeGrant {
    const clientId = normalizeClientId(input.clientId);
    const record = this.authorizationCodes.get(input.code);
    if (!record) {
      fail("invalid_grant", "The authorization code is invalid or has already been used.");
    }
    if (record.expiresAt <= this.now()) {
      this.authorizationCodes.delete(input.code);
      fail("invalid_grant", "The authorization code has expired.");
    }
    if (record.clientId !== clientId) {
      fail("invalid_grant", "The authorization code was not issued to this client.");
    }

    let resolved: ResolvedRedirectUri;
    try {
      resolved = this.resolveRedirectUri(clientId, input.redirectUri);
    } catch (error) {
      if (error instanceof OAuthCallbackError) {
        fail("invalid_grant", error.message);
      }
      throw error;
    }

    if (resolved.redirectUri !== record.redirectUri) {
      fail(
        "invalid_grant",
        "The redirect URI does not match the URI bound to the authorization code.",
      );
    }

    // Re-check the originally bound URI as well, so changing the client's
    // registration between authorization and token exchange invalidates it.
    try {
      const rebound = this.resolveRedirectUri(clientId, record.redirectUri);
      if (rebound.redirectUri !== record.redirectUri) {
        fail("invalid_grant", "The registered redirect URI has changed.");
      }
    } catch (error) {
      if (error instanceof OAuthCallbackError) {
        fail("invalid_grant", error.message);
      }
      throw error;
    }

    if (
      typeof input.codeVerifier !== "string" ||
      !verifyPkce(input.codeVerifier, record.codeChallenge, record.codeChallengeMethod)
    ) {
      fail("invalid_grant", "The PKCE code_verifier is invalid.");
    }

    if ((record.nonce ?? "") !== (input.nonce ?? "")) {
      fail("invalid_grant", "The nonce does not match the authorization request.");
    }

    this.authorizationCodes.delete(input.code);

    const grant: AuthorizationCodeGrant = {
      clientId: record.clientId,
      redirectUri: record.redirectUri,
      codeChallenge: record.codeChallenge,
      codeChallengeMethod: record.codeChallengeMethod,
    };
    if (record.nonce !== undefined) {
      return { ...grant, nonce: record.nonce };
    }
    return grant;
  }

  issueAuthorizationCode(input: AuthorizeInput): IssuedAuthorizationCode {
    return this.authorize(input);
  }

  exchangeAuthorizationCode(input: ExchangeAuthorizationCodeInput): AuthorizationCodeGrant {
    return this.exchange(input);
  }

  private requireClient(clientId: string): RegisteredClient {
    const client = this.clients.get(clientId);
    if (!client) {
      fail("invalid_client", "Unknown client.");
    }
    return client;
  }
}

function redirectUriPartsMatch(
  registered: ParsedRedirectUri,
  requested: ParsedRedirectUri,
): boolean {
  return (
    registered.scheme === requested.scheme &&
    registered.host === requested.host &&
    registered.port === requested.port &&
    registered.query === requested.query &&
    pathSegmentsMatch(registered, requested)
  );
}

function pathSegmentsMatch(
  registered: ParsedRedirectUri,
  requested: ParsedRedirectUri,
): boolean {
  if (registered.pathSegments.length !== requested.pathSegments.length) {
    return false;
  }
  for (let index = 0; index < registered.pathSegments.length; index += 1) {
    const registeredSegment = registered.pathSegments[index];
    const requestedSegment = requested.pathSegments[index];
    if (registeredSegment === "*") {
      if (
        requestedSegment.length === 0 ||
        requestedSegment === "." ||
        requestedSegment === ".."
      ) {
        return false;
      }
      continue;
    }
    if (registeredSegment !== requestedSegment) {
      return false;
    }
  }
  return true;
}

export function createOAuthCallbackValidator(
  options?: OAuthCallbackValidatorOptions,
): OAuthCallbackValidator {
  return new OAuthCallbackValidator(options);
}

export function createCallbackValidator(
  options?: OAuthCallbackValidatorOptions,
): OAuthCallbackValidator {
  return new OAuthCallbackValidator(options);
}

export function createAuthorizationServer(
  options?: OAuthCallbackValidatorOptions,
): OAuthCallbackValidator {
  return new OAuthCallbackValidator(options);
}

export {
  OAuthCallbackValidator as AuthorizationServer,
  OAuthCallbackValidator as CallbackValidator,
  OAuthCallbackValidator as RedirectUriValidator,
};

export function compareRedirectUris(
  registeredUri: string,
  requestedUri: string,
  policy?: RedirectUriPolicy,
): boolean {
  return matchRedirectUri(registeredUri, requestedUri, policy) !== null;
}
