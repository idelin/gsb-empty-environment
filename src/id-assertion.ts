import {
  validationError,
  validationOk,
  type FedCMResult,
} from "./errors.ts";
import {
  parseOrigin,
  requestHeaders,
  type FedCMHttpRequest,
} from "./request.ts";
import type { HeaderBag } from "./headers.ts";
import type {
  ClientMetadata,
  ClientMetadataSource,
  FedCMAccount,
  FedCMSession,
  SessionSource,
} from "./types.ts";
import { getAccountId } from "./util.ts";
import {
  buildIdAssertionClaims,
  normalizeDisclosureTextShown,
} from "./disclosure.ts";

export interface IdAssertionHttpRequest extends FedCMHttpRequest {
  /**
   * Parsed JSON object, URLSearchParams, FormData, a raw JSON/form string, or
   * an iterable of key/value pairs.
   */
  body?: unknown;
}

export interface IdAssertionRequestOptions {
  clientMetadata?: ClientMetadataSource;
  /** Alias for `clientMetadata`. */
  metadata?: ClientMetadataSource;
  session?: SessionSource;
  /** Convenience alternative to `session` when the handler already has them. */
  accounts?: readonly FedCMAccount[];
  /** id_assertion requests are credentialed; defaults to `true`. */
  requireCookie?: boolean;
  /** Optionally require `Sec-Fetch-Dest: webidentity` as well. */
  requireWebIdentity?: boolean;
}

export interface ParsedIdAssertionBody {
  clientId: string | null;
  accountId: string | null;
  disclosureTextShown: boolean;
  disclosureTextShownPresent: boolean;
  values: Readonly<Record<string, unknown>>;
}

export interface ValidatedIdAssertionRequest {
  endpoint: "id_assertion";
  method: "POST";
  origin: string;
  clientId: string;
  clientMetadata: ClientMetadata;
  account: FedCMAccount;
  accountId: string;
  session: FedCMSession;
  disclosureTextShown: boolean;
  emailDisclosureAllowed: boolean;
  headers: HeaderBag;
}

function isIterable(value: unknown): value is Iterable<unknown> {
  if (typeof value !== "object" || value === null) return false;
  const iterator = (value as { [Symbol.iterator]?: unknown })[
    Symbol.iterator
  ];
  return typeof iterator === "function";
}

function invalidBody(message: string): never {
  throw new Error(message);
}

function toRecord(input: unknown): Record<string, unknown> {
  if (input === null || input === undefined) {
    return invalidBody("id_assertion request body is missing");
  }

  if (typeof input === "string") {
    const text = input.trim();
    if (!text) return invalidBody("id_assertion request body is empty");

    if (text.startsWith("{")) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch {
        return invalidBody("id_assertion request body is not valid JSON");
      }
      if (
        typeof parsed !== "object" ||
        parsed === null ||
        Array.isArray(parsed)
      ) {
        return invalidBody("id_assertion JSON body must be an object");
      }
      return { ...(parsed as Record<string, unknown>) };
    }

    if (text.startsWith("[")) {
      return invalidBody("id_assertion request body must be an object");
    }

    return Object.fromEntries(new URLSearchParams(text));
  }

  if (Array.isArray(input)) {
    return invalidBody("id_assertion request body must not be an array");
  }

  if (isIterable(input)) {
    const record: Record<string, unknown> = {};
    for (const pair of input) {
      if (!Array.isArray(pair) || pair.length < 2) continue;
      const key: unknown = pair[0];
      const value: unknown = pair[1];
      if (typeof key === "string") record[key] = value;
    }
    return record;
  }

  if (typeof input === "object") {
    return { ...(input as Record<string, unknown>) };
  }

  return invalidBody("id_assertion request body must be an object");
}

function readIdentifier(value: unknown): string | null {
  if (typeof value === "string" && value.length > 0) return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return null;
}

/**
 * Parses the id_assertion body. Both JSON and the form encoding used by
 * browsers are supported. A missing disclosure marker is normalized to `false`
 * and therefore cannot unlock email disclosure.
 */
export function parseIdAssertionBody(input: unknown): ParsedIdAssertionBody {
  const values = toRecord(input);
  const hasDisclosureText = Object.prototype.hasOwnProperty.call(
    values,
    "disclosure_text_shown",
  );

  return {
    clientId: readIdentifier(values.client_id),
    accountId: readIdentifier(values.account_id),
    disclosureTextShown: normalizeDisclosureTextShown(
      values.disclosure_text_shown,
    ),
    disclosureTextShownPresent: hasDisclosureText,
    values,
  };
}

function isMapLike(
  value: unknown,
): value is ReadonlyMap<string, ClientMetadata> {
  if (typeof value !== "object" || value === null) return false;
  const candidate = value as ReadonlyMap<string, ClientMetadata>;
  return (
    typeof candidate.get === "function" &&
    typeof candidate.has === "function"
  );
}

function resolveClientMetadata(
  source: ClientMetadataSource | undefined,
  clientId: string,
): ClientMetadata | null {
  if (!source) return null;

  if (typeof source === "function") {
    const found = source(clientId);
    if (!found) return null;
    if (found.client_id && found.client_id !== clientId) return null;
    return found;
  }

  if (Array.isArray(source)) {
    return source.find((entry) => entry.client_id === clientId) ?? null;
  }

  if (isMapLike(source)) {
    const found = source.get(clientId);
    if (!found) return null;
    if (found.client_id && found.client_id !== clientId) return null;
    return found;
  }

  const metadata = source as ClientMetadata;
  return metadata.client_id === clientId ? metadata : null;
}

function allowedOrigins(metadata: ClientMetadata): Set<string> {
  const raw: unknown = metadata.origins;
  const entries: unknown[] =
    typeof raw === "string" ? [raw] : Array.isArray(raw) ? raw : [];
  const result = new Set<string>();

  for (const entry of entries) {
    if (typeof entry !== "string") continue;
    const origin = parseOrigin(entry);
    if (origin) result.add(origin);
  }

  return result;
}

function validateWebIdentityDestination(
  headers: HeaderBag,
): FedCMResult<null> {
  const values = headers.getAll("sec-fetch-dest");
  if (values.length === 0) {
    return validationError(
      "missing_sec_fetch_dest",
      "id_assertion requests must include Sec-Fetch-Dest: webidentity",
    );
  }

  const normalized = values
    .map((value) => value.trim().toLowerCase())
    .filter((value) => value.length > 0);

  if (normalized.length !== 1 || normalized[0] !== "webidentity") {
    return validationError(
      "invalid_sec_fetch_dest",
      "id_assertion requests must have exactly Sec-Fetch-Dest: webidentity",
    );
  }

  return validationOk(null);
}

function firstOrigin(
  headers: HeaderBag,
): FedCMResult<string> {
  const values = headers.getAll("origin");
  if (values.length === 0 || values.every((value) => value.trim() === "")) {
    return validationError(
      "missing_origin",
      "id_assertion requests must include an Origin header",
    );
  }

  if (values.length > 1 || values[0].includes(",")) {
    return validationError(
      "multiple_origins",
      "id_assertion requests must include exactly one Origin header",
    );
  }

  const origin = parseOrigin(values[0]);
  if (!origin) {
    return validationError(
      "invalid_origin",
      "id_assertion Origin must be a valid http(s) origin",
    );
  }

  return validationOk(origin);
}

/**
 * Validates a request to the FedCM id_assertion endpoint.
 *
 * - only POST is accepted;
 * - the Origin header must match an origin registered for the body's
 *   `client_id`;
 * - the body's `account_id` must belong to the session selected by Cookie;
 * - `disclosure_text_shown` is returned so the caller can gate email claims.
 */
export function validateIdAssertionRequest(
  request: IdAssertionHttpRequest,
  options: IdAssertionRequestOptions,
): FedCMResult<ValidatedIdAssertionRequest> {
  const headers = requestHeaders(request);
  const method = (request.method ?? "").toUpperCase();

  if (method !== "POST") {
    return validationError(
      "method_not_allowed",
      `id_assertion endpoint only accepts POST, got ${method || "(missing)"}`,
      405,
    );
  }

  if (options.requireWebIdentity === true) {
    const destination = validateWebIdentityDestination(headers);
    if (!destination.ok) return destination;
  }

  const originResult = firstOrigin(headers);
  if (!originResult.ok) return originResult;
  const origin = originResult.value;

  let body: ParsedIdAssertionBody;
  try {
    body = parseIdAssertionBody(request.body);
  } catch (error) {
    return validationError(
      "invalid_body",
      error instanceof Error
        ? error.message
        : "id_assertion request body is invalid",
      400,
    );
  }

  if (!body.clientId) {
    return validationError(
      "missing_client_id",
      "id_assertion body must include client_id",
      400,
    );
  }

  const metadataSource = options.clientMetadata ?? options.metadata;
  if (!metadataSource) {
    return validationError(
      "unknown_client",
      `No client metadata is configured for ${body.clientId}`,
    );
  }

  const metadata = resolveClientMetadata(metadataSource, body.clientId);
  if (!metadata) {
    return validationError(
      "unknown_client",
      `Unknown client_id: ${body.clientId}`,
    );
  }

  if (!allowedOrigins(metadata).has(origin)) {
    return validationError(
      "origin_not_allowed",
      `Origin ${origin} is not registered for client_id ${body.clientId}`,
    );
  }

  if (!body.accountId) {
    return validationError(
      "missing_account_id",
      "id_assertion body must include account_id",
      400,
    );
  }

  const requireCookie = options.requireCookie !== false;
  const cookieHeader = headers.getAll("cookie").join("; ");
  if (
    requireCookie &&
    (!headers.has("cookie") || cookieHeader.trim() === "")
  ) {
    return validationError(
      "missing_cookie",
      "id_assertion requests must include the session Cookie",
      401,
    );
  }

  let session: FedCMSession | null | undefined;
  if (typeof options.session === "function") {
    session = options.session(
      cookieHeader.trim() === "" ? null : cookieHeader,
    );
  } else if (options.session) {
    session = options.session;
  } else if (options.accounts) {
    session = { accounts: options.accounts };
  } else {
    session = null;
  }

  if (!session) {
    return validationError(
      "session_not_found",
      "No session was found for the supplied Cookie",
      401,
    );
  }

  if (!Array.isArray(session.accounts)) {
    return validationError(
      "session_not_found",
      "The session does not contain an account list",
      401,
    );
  }

  const account = session.accounts.find(
    (candidate) => getAccountId(candidate) === body.accountId,
  );
  if (!account) {
    return validationError(
      "account_not_found",
      `account_id ${body.accountId} does not belong to this session`,
    );
  }

  return validationOk({
    endpoint: "id_assertion",
    method: "POST",
    origin,
    clientId: body.clientId,
    clientMetadata: metadata,
    account,
    accountId: body.accountId,
    session,
    disclosureTextShown: body.disclosureTextShown,
    emailDisclosureAllowed: body.disclosureTextShown,
    headers,
  });
}

export function assertIdAssertionRequest(
  request: IdAssertionHttpRequest,
  options: IdAssertionRequestOptions,
): ValidatedIdAssertionRequest {
  const result = validateIdAssertionRequest(request, options);
  if (!result.ok) throw result.error;
  return result.value;
}

/**
 * Convenience for handlers: validate once, then build a response whose email
 * claims obey the validated disclosure signal.
 */
export function createIdAssertionClaimsFromRequest(
  validated: ValidatedIdAssertionRequest,
  claims?: Readonly<Record<string, unknown>>,
): Record<string, unknown> {
  return buildIdAssertionClaims({
    account: validated.account,
    disclosureTextShown: validated.disclosureTextShown,
    claims,
  });
}
