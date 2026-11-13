import { createHmac, timingSafeEqual } from "node:crypto";

export const ONE_CLICK_BODY = "List-Unsubscribe=One-Click";
export const FORM_CONTENT_TYPE = "application/x-www-form-urlencoded";
export const DEFAULT_UNSUBSCRIBE_PATH = "/unsubscribe";

const HEX_SHA256_RE = /^[0-9a-f]{64}$/i;
const UNSIGNED_DECIMAL_RE = /^[0-9]+$/;
const NUL_RE = /\u0000/;

export type UnsubscribeSecret = string | Uint8Array;
export type ExpiryValue = number | string | Date;
export type NowValue = number | Date | (() => number | Date);

export interface UnsubscribeTokenInput {
  secret: UnsubscribeSecret;
  recipient?: string;
  email?: string;
  listId?: string;
  list?: string;
  expiresAt?: ExpiryValue;
  expires?: ExpiryValue;
  expiry?: ExpiryValue;
}

export interface VerifyUnsubscribeTokenInput {
  secret: UnsubscribeSecret;
  recipient?: string;
  email?: string;
  listId?: string;
  list?: string;
  token: string;
  expiresAt?: ExpiryValue;
  expires?: ExpiryValue;
  expiry?: ExpiryValue;
  expiresText?: string;
  now?: NowValue;
}

export type UnsubscribeTokenErrorCode =
  | "invalid_input"
  | "invalid_token"
  | "expired";

export interface UnsubscribeTokenValidation {
  ok: boolean;
  code?: UnsubscribeTokenErrorCode;
  reason?: string;
  listId?: string;
  recipient?: string;
  expiresText?: string;
  expiresAt?: number;
  now?: number;
}

export interface CreateUnsubscribeUrlInput {
  baseUrl: string | URL;
  secret: UnsubscribeSecret;
  recipient?: string;
  email?: string;
  listId?: string;
  list?: string;
  expiresAt?: ExpiryValue;
  expires?: ExpiryValue;
  expiry?: ExpiryValue;
  path?: string;
  pathPrefix?: string;
}

export interface UnsubscribeEvent {
  listId: string;
  recipient: string;
  expiresAt: number;
  expiresText: string;
  token: string;
  request: unknown;
}

export interface UnsubscribeStore {
  unsubscribe(
    listId: string,
    recipient: string,
    event: UnsubscribeEvent,
  ): void | Promise<void>;
}

export interface OneClickUnsubscribeHandlerOptions {
  secret: UnsubscribeSecret;
  onUnsubscribe?: (event: UnsubscribeEvent) => void | Promise<void>;
  unsubscribe?: (event: UnsubscribeEvent) => void | Promise<void>;
  store?: UnsubscribeStore;
  now?: NowValue;
  path?: string;
  pathPrefix?: string;
  confirmPage?: (event: Omit<UnsubscribeEvent, "token" | "request">) => string;
  successPage?: (event: Omit<UnsubscribeEvent, "token" | "request">) => string;
}

export interface UnsubscribeHttpRequest {
  method?: string;
  url?: string | URL;
  originalUrl?: string;
  headers?:
    | Headers
    | Record<string, string | readonly string[] | undefined>
    | { get(name: string): string | null };
  body?: unknown;
  text?: () => string | Promise<string>;
}

export type UnsubscribeRequest = Request | UnsubscribeHttpRequest;

export type HeaderValue = string | readonly string[];
export type OutboundHeaders =
  | Record<string, HeaderValue | undefined>
  | readonly { name: string; value: HeaderValue }[];

export type SignedHeaderSet = string | readonly string[] | ReadonlySet<string>;

export interface DkimOptions {
  signedHeaders?: SignedHeaderSet;
  headers?: SignedHeaderSet;
  h?: SignedHeaderSet;
  signature?: string;
  header?: string;
}

export interface OutboundEmailInput {
  [key: string]: unknown;
  headers?: OutboundHeaders;
  dkim?: DkimOptions | SignedHeaderSet;
  dkimSignature?: string;
  dkimSignedHeaders?: SignedHeaderSet;
  signedHeaders?: SignedHeaderSet;
}

export const REQUIRED_UNSUBSCRIBE_DKIM_HEADERS = [
  "List-Unsubscribe",
  "List-Unsubscribe-Post",
] as const;

export class DkimHeaderCoverageError extends Error {
  readonly code = "DKIM_HEADERS_MISSING";
  readonly missingHeaders: readonly string[];

  constructor(missingHeaders: readonly string[]) {
    super(
      `DKIM signing must cover ${REQUIRED_UNSUBSCRIBE_DKIM_HEADERS.join(
        " and ",
      )}; missing: ${missingHeaders.join(", ")}`,
    );
    this.name = "DkimHeaderCoverageError";
    this.missingHeaders = missingHeaders;
  }
}

function assertSecret(secret: UnsubscribeSecret): void {
  if (typeof secret === "string") {
    if (secret.length === 0) {
      throw new TypeError("secret must not be empty");
    }
    return;
  }

  if (secret instanceof Uint8Array) {
    if (secret.byteLength === 0) {
      throw new TypeError("secret must not be empty");
    }
    return;
  }

  throw new TypeError("secret must be a string or Uint8Array");
}

function expiryText(value: ExpiryValue | undefined): string {
  if (value === undefined) {
    throw new TypeError("expiresAt is required");
  }

  let text: string;

  if (typeof value === "string") {
    text = value;
  } else if (value instanceof Date) {
    const milliseconds = value.getTime();
    if (!Number.isFinite(milliseconds)) {
      throw new TypeError("expiresAt must be a valid date");
    }
    text = String(Math.floor(milliseconds / 1000));
  } else if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new TypeError(
        "expiresAt must be a non-negative integer number of seconds",
      );
    }
    text = String(value);
  } else {
    throw new TypeError("expiresAt must be seconds, a date, or decimal text");
  }

  if (!UNSIGNED_DECIMAL_RE.test(text)) {
    throw new TypeError("expiresAt must be unsigned decimal text");
  }

  const seconds = Number(text);
  if (!Number.isSafeInteger(seconds) || seconds < 0) {
    throw new TypeError("expiresAt is outside the safe integer range");
  }

  return text;
}

function assertBindingPart(value: string | undefined, name: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError(`${name} is required`);
  }
  if (NUL_RE.test(value)) {
    throw new TypeError(`${name} must not contain a NUL byte`);
  }
  return value;
}

function firstDefined<T>(...values: (T | undefined)[]): T | undefined {
  for (const value of values) {
    if (value !== undefined) {
      return value;
    }
  }
  return undefined;
}

function tokenArguments(
  inputOrSecret: UnsubscribeTokenInput | UnsubscribeSecret,
  recipientArg?: string,
  listIdArg?: string,
  expiresAtArg?: ExpiryValue,
): {
  secret: UnsubscribeSecret;
  recipient: string;
  listId: string;
  expiresText: string;
} {
  if (typeof inputOrSecret === "string" || inputOrSecret instanceof Uint8Array) {
    assertSecret(inputOrSecret);
    return {
      secret: inputOrSecret,
      recipient: assertBindingPart(recipientArg, "recipient"),
      listId: assertBindingPart(listIdArg, "listId"),
      expiresText: expiryText(expiresAtArg),
    };
  }

  if (inputOrSecret === null || typeof inputOrSecret !== "object") {
    throw new TypeError("token options must be an object");
  }

  assertSecret(inputOrSecret.secret);
  const recipient = firstDefined(inputOrSecret.recipient, inputOrSecret.email);
  const listId = firstDefined(inputOrSecret.listId, inputOrSecret.list);
  const expires = firstDefined(
    inputOrSecret.expiresAt,
    inputOrSecret.expires,
    inputOrSecret.expiry,
  );

  return {
    secret: inputOrSecret.secret,
    recipient: assertBindingPart(recipient, "recipient"),
    listId: assertBindingPart(listId, "listId"),
    expiresText: expiryText(expires),
  };
}

export function buildUnsubscribeTokenMessage(
  recipient: string,
  listId: string,
  expiresText: string,
): string {
  const validRecipient = assertBindingPart(recipient, "recipient");
  const validListId = assertBindingPart(listId, "listId");
  if (!UNSIGNED_DECIMAL_RE.test(expiresText)) {
    throw new TypeError("expiresText must be unsigned decimal text");
  }

  return `${validRecipient}\u0000${validListId}\u0000${expiresText}`;
}

export function createUnsubscribeToken(
  input: UnsubscribeTokenInput,
): string;
export function createUnsubscribeToken(
  secret: UnsubscribeSecret,
  recipient: string,
  listId: string,
  expiresAt: ExpiryValue,
): string;
export function createUnsubscribeToken(
  inputOrSecret: UnsubscribeTokenInput | UnsubscribeSecret,
  recipientArg?: string,
  listIdArg?: string,
  expiresAtArg?: ExpiryValue,
): string {
  const args = tokenArguments(
    inputOrSecret,
    recipientArg,
    listIdArg,
    expiresAtArg,
  );
  const message = buildUnsubscribeTokenMessage(
    args.recipient,
    args.listId,
    args.expiresText,
  );

  return createHmac("sha256", args.secret).update(message, "utf8").digest("hex");
}

export function constantTimeEqualHex(
  expectedHex: string,
  candidateHex: string,
): boolean {
  if (
    !HEX_SHA256_RE.test(expectedHex) ||
    !HEX_SHA256_RE.test(candidateHex)
  ) {
    return false;
  }

  const expected = Buffer.from(expectedHex, "hex");
  const candidate = Buffer.from(candidateHex, "hex");
  return (
    expected.byteLength === candidate.byteLength &&
    timingSafeEqual(expected, candidate)
  );
}

function resolveNow(now: NowValue | undefined): number {
  const value =
    typeof now === "function"
      ? now()
      : now === undefined
        ? Date.now()
        : now;

  const seconds =
    value instanceof Date ? Math.floor(value.getTime() / 1000) : value;

  if (!Number.isSafeInteger(seconds) || seconds < 0) {
    throw new TypeError("now must resolve to non-negative integer seconds");
  }

  return seconds;
}

export function inspectUnsubscribeToken(
  input: VerifyUnsubscribeTokenInput,
): UnsubscribeTokenValidation {
  try {
    assertSecret(input.secret);

    const recipient = assertBindingPart(
      firstDefined(input.recipient, input.email),
      "recipient",
    );
    const listId = assertBindingPart(
      firstDefined(input.listId, input.list),
      "listId",
    );
    const expiresText =
      input.expiresText ??
      expiryText(
        firstDefined(input.expiresAt, input.expires, input.expiry),
      );

    if (typeof input.token !== "string" || input.token.length === 0) {
      return { ok: false, code: "invalid_token", reason: "token is required" };
    }

    const expected = createUnsubscribeToken({
      secret: input.secret,
      recipient,
      listId,
      expiresAt: expiresText,
    });

    if (!constantTimeEqualHex(expected, input.token)) {
      return {
        ok: false,
        code: "invalid_token",
        reason: "token does not match the signing parameters",
        listId,
        recipient,
        expiresText,
      };
    }

    const expiresAt = Number(expiresText);
    if (!Number.isSafeInteger(expiresAt)) {
      return {
        ok: false,
        code: "invalid_input",
        reason: "expiresAt is outside the safe integer range",
        listId,
        recipient,
        expiresText,
      };
    }

    const now = resolveNow(input.now);
    if (now >= expiresAt) {
      return {
        ok: false,
        code: "expired",
        reason: "token has expired",
        listId,
        recipient,
        expiresText,
        expiresAt,
        now,
      };
    }

    return {
      ok: true,
      listId,
      recipient,
      expiresText,
      expiresAt,
      now,
    };
  } catch (error) {
    return {
      ok: false,
      code: "invalid_input",
      reason: error instanceof Error ? error.message : "invalid input",
    };
  }
}

export function verifyUnsubscribeToken(
  input: VerifyUnsubscribeTokenInput,
): boolean;
export function verifyUnsubscribeToken(
  token: string,
  input: Omit<VerifyUnsubscribeTokenInput, "token">,
): boolean;
export function verifyUnsubscribeToken(
  inputOrToken: VerifyUnsubscribeTokenInput | string,
  maybeInput?: Omit<VerifyUnsubscribeTokenInput, "token">,
): boolean {
  if (typeof inputOrToken === "string") {
    if (maybeInput === undefined) {
      throw new TypeError("verification options are required");
    }
    return inspectUnsubscribeToken({
      ...maybeInput,
      token: inputOrToken,
    }).ok;
  }

  return inspectUnsubscribeToken(inputOrToken).ok;
}

function normalizePathPrefix(path: string | undefined): string {
  const raw = path ?? DEFAULT_UNSUBSCRIBE_PATH;
  if (typeof raw !== "string" || raw.length === 0) {
    throw new TypeError("unsubscribe path must not be empty");
  }

  let normalized = raw.startsWith("/") ? raw : `/${raw}`;
  normalized = normalized.replace(/\/+$/, "");
  return normalized || "/";
}

function appendListId(prefix: string, listId: string): string {
  const encodedListId = encodeURIComponent(listId);
  return prefix === "/" ? `/${encodedListId}` : `${prefix}/${encodedListId}`;
}

export function createOneClickUnsubscribeUrl(
  input: CreateUnsubscribeUrlInput,
): string;
export function createOneClickUnsubscribeUrl(
  baseUrl: string | URL,
  input: Omit<CreateUnsubscribeUrlInput, "baseUrl">,
): string;
export function createOneClickUnsubscribeUrl(
  inputOrBaseUrl: CreateUnsubscribeUrlInput | string | URL,
  maybeInput?: Omit<CreateUnsubscribeUrlInput, "baseUrl">,
): string {
  const input: CreateUnsubscribeUrlInput =
    typeof inputOrBaseUrl === "string" || inputOrBaseUrl instanceof URL
      ? { ...(maybeInput ?? {}), baseUrl: inputOrBaseUrl }
      : inputOrBaseUrl;

  if (input === null || typeof input !== "object") {
    throw new TypeError("URL options must be an object");
  }

  const baseUrl = new URL(String(input.baseUrl));
  const pathOption = firstDefined(input.path, input.pathPrefix);
  const prefix = normalizePathPrefix(
    pathOption ??
      (baseUrl.pathname !== "/" ? baseUrl.pathname : DEFAULT_UNSUBSCRIBE_PATH),
  );
  const recipient = assertBindingPart(
    firstDefined(input.recipient, input.email),
    "recipient",
  );
  const listId = assertBindingPart(
    firstDefined(input.listId, input.list),
    "listId",
  );
  const expiresText = expiryText(
    firstDefined(input.expiresAt, input.expires, input.expiry),
  );
  const token = createUnsubscribeToken({
    secret: input.secret,
    recipient,
    listId,
    expiresAt: expiresText,
  });

  const url = new URL(baseUrl.toString());
  url.pathname = appendListId(prefix, listId);
  url.search = "";
  url.hash = "";
  url.searchParams.set("recipient", recipient);
  url.searchParams.set("expires", expiresText);
  url.searchParams.set("token", token);

  return url.toString();
}

function requestUrl(request: UnsubscribeRequest): URL {
  const raw =
    "url" in request && request.url !== undefined
      ? request.url
      : "originalUrl" in request
        ? request.originalUrl
        : undefined;

  if (raw === undefined) {
    throw new TypeError("request URL is required");
  }

  if (raw instanceof URL) {
    return new URL(raw.toString());
  }

  return new URL(String(raw), "http://localhost");
}

function requestMethod(request: UnsubscribeRequest): string {
  const method = "method" in request ? request.method : undefined;
  return (method ?? "GET").toUpperCase();
}

function headerValue(
  headers: UnsubscribeHttpRequest["headers"],
  name: string,
): string | null {
  if (headers === undefined || headers === null) {
    return null;
  }

  if (typeof (headers as { get?: unknown }).get === "function") {
    return (headers as { get(name: string): string | null }).get(name);
  }

  const wanted = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() !== wanted || value === undefined) {
      continue;
    }
    return Array.isArray(value) ? value.join(", ") : String(value);
  }

  return null;
}

async function requestBodyText(request: UnsubscribeRequest): Promise<string> {
  if (typeof request.text === "function") {
    return request.text();
  }

  const body = "body" in request ? request.body : undefined;
  if (body === undefined || body === null) {
    return "";
  }
  if (typeof body === "string") {
    return body;
  }
  if (body instanceof URLSearchParams) {
    return body.toString();
  }
  if (body instanceof Uint8Array) {
    return new TextDecoder().decode(body);
  }

  throw new TypeError("request body must be text");
}

function pathListId(url: URL, prefix: string): string | undefined {
  const pathname = url.pathname;
  if (pathname === prefix || pathname === `${prefix}/`) {
    return undefined;
  }

  const pathPrefix = prefix === "/" ? "/" : `${prefix}/`;
  if (!pathname.startsWith(pathPrefix)) {
    throw new TypeError("request path does not match the unsubscribe endpoint");
  }

  const encoded = pathname.slice(pathPrefix.length);
  if (encoded.length === 0 || encoded.endsWith("/")) {
    throw new TypeError("list ID is missing from the request path");
  }

  let decoded: string;
  try {
    decoded = decodeURIComponent(encoded);
  } catch {
    throw new TypeError("list ID in the request path is malformed");
  }

  return assertBindingPart(decoded, "listId");
}

function uniqueQueryValue(
  searchParams: URLSearchParams,
  names: readonly string[],
): string | undefined {
  const values = names.flatMap((name) => searchParams.getAll(name));
  if (values.length > 1) {
    throw new TypeError(`duplicate ${names[0]} parameter`);
  }
  return values[0];
}

interface ParsedUnsubscribeTarget {
  listId: string;
  recipient: string;
  expiresText: string;
  token: string;
}

function parseUnsubscribeTarget(
  url: URL,
  prefix: string,
): ParsedUnsubscribeTarget | { error: string } {
  try {
    const pathList = pathListId(url, prefix);
    const queryListId = uniqueQueryValue(url.searchParams, [
      "listId",
      "list",
      "list_id",
    ]);

    if (pathList !== undefined && queryListId !== undefined) {
      if (pathList !== queryListId) {
        return { error: "list ID in path and query does not match" };
      }
    }

    const listId = pathList ?? queryListId;
    if (listId === undefined) {
      return { error: "list ID is required" };
    }

    const recipient = uniqueQueryValue(url.searchParams, [
      "recipient",
      "email",
      "address",
    ]);
    if (recipient === undefined) {
      return { error: "recipient is required" };
    }

    const expiresText = uniqueQueryValue(url.searchParams, [
      "expires",
      "expiresAt",
      "expiry",
      "exp",
    ]);
    if (expiresText === undefined) {
      return { error: "expires is required" };
    }

    const token = uniqueQueryValue(url.searchParams, [
      "token",
      "sig",
      "signature",
    ]);
    if (token === undefined) {
      return { error: "token is required" };
    }

    return {
      listId: assertBindingPart(listId, "listId"),
      recipient: assertBindingPart(recipient, "recipient"),
      expiresText,
      token,
    };
  } catch (error) {
    return {
      error: error instanceof Error ? error.message : "invalid request URL",
    };
  }
}

function isFormContentType(value: string | null): boolean {
  if (value === null) {
    return false;
  }
  const mediaType = value.split(";", 1)[0]?.trim().toLowerCase();
  return mediaType === FORM_CONTENT_TYPE;
}

function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function defaultConfirmPage(
  event: Omit<UnsubscribeEvent, "token" | "request">,
): string {
  const recipient = escapeHtml(event.recipient);
  const listId = escapeHtml(event.listId);
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Confirm unsubscribe</title>
</head>
<body>
<main>
<h1>Confirm unsubscribe</h1>
<p>Unsubscribe <strong>${recipient}</strong> from <strong>${listId}</strong>?</p>
<form method="POST">
<input type="hidden" name="List-Unsubscribe" value="One-Click">
<button type="submit">Confirm unsubscribe</button>
</form>
<p>Opening this page did not change your subscription. Confirmation requires the button above.</p>
</main>
</body>
</html>`;
}

function defaultSuccessPage(
  event: Omit<UnsubscribeEvent, "token" | "request">,
): string {
  return `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Unsubscribed</title></head>
<body><main><h1>Unsubscribed</h1><p>${escapeHtml(
    event.recipient,
  )} has been unsubscribed from ${escapeHtml(event.listId)}.</p></main></body>
</html>`;
}

function htmlResponse(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
      "Content-Security-Policy":
        "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
    },
  });
}

function errorResponse(
  status: number,
  message: string,
  headers?: HeadersInit,
): Response {
  return new Response(message, {
    status,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
      ...headers,
    },
  });
}

function resolveUnsubscribeCallback(
  options: OneClickUnsubscribeHandlerOptions,
): ((event: UnsubscribeEvent) => void | Promise<void>) | undefined {
  if (options.onUnsubscribe !== undefined) {
    return options.onUnsubscribe;
  }
  if (options.unsubscribe !== undefined) {
    return options.unsubscribe;
  }
  if (options.store !== undefined && options.store !== null) {
    return (event) =>
      options.store!.unsubscribe(event.listId, event.recipient, event);
  }
  return undefined;
}

export interface OneClickUnsubscribeHandler {
  (request: UnsubscribeRequest): Promise<Response>;
  handle(request: UnsubscribeRequest): Promise<Response>;
}

export function createOneClickUnsubscribeHandler(
  options: OneClickUnsubscribeHandlerOptions,
): OneClickUnsubscribeHandler {
  if (options === null || typeof options !== "object") {
    throw new TypeError("handler options are required");
  }
  assertSecret(options.secret);

  const prefix = normalizePathPrefix(
    firstDefined(options.path, options.pathPrefix),
  );
  const callback = resolveUnsubscribeCallback(options);

  const handle = async (request: UnsubscribeRequest): Promise<Response> => {
    const method = requestMethod(request);

    let url: URL;
    try {
      url = requestUrl(request);
    } catch {
      return errorResponse(400, "Invalid unsubscribe request URL.");
    }

    const target = parseUnsubscribeTarget(url, prefix);
    if ("error" in target) {
      return errorResponse(400, `Invalid unsubscribe link: ${target.error}.`);
    }

    const validation = inspectUnsubscribeToken({
      secret: options.secret,
      recipient: target.recipient,
      listId: target.listId,
      expiresText: target.expiresText,
      token: target.token,
      now: options.now,
    });

    if (!validation.ok) {
      return errorResponse(400, "Invalid or expired unsubscribe link.");
    }

    const event: UnsubscribeEvent = {
      listId: target.listId,
      recipient: target.recipient,
      expiresAt: validation.expiresAt!,
      expiresText: target.expiresText,
      token: target.token,
      request,
    };

    if (method === "GET") {
      const page = (options.confirmPage ?? defaultConfirmPage)({
        listId: event.listId,
        recipient: event.recipient,
        expiresAt: event.expiresAt,
        expiresText: event.expiresText,
      });
      return htmlResponse(page, 200);
    }

    if (method !== "POST") {
      return errorResponse(405, "Use GET to confirm or POST to unsubscribe.", {
        Allow: "GET, POST",
      });
    }

    if (!isFormContentType(headerValue(request.headers, "content-type"))) {
      return errorResponse(
        415,
        `Content-Type must be ${FORM_CONTENT_TYPE}.`,
      );
    }

    let body: string;
    try {
      body = await requestBodyText(request);
    } catch {
      return errorResponse(400, "Unsubscribe request body must be text.");
    }

    if (body !== ONE_CLICK_BODY) {
      return errorResponse(
        400,
        `Unsubscribe request body must be exactly ${ONE_CLICK_BODY}.`,
      );
    }

    if (callback === undefined) {
      return errorResponse(500, "Unsubscribe handler has no store configured.");
    }

    try {
      await callback(event);
    } catch {
      return errorResponse(500, "Unsubscribe could not be recorded.");
    }

    const page = (options.successPage ?? defaultSuccessPage)({
      listId: event.listId,
      recipient: event.recipient,
      expiresAt: event.expiresAt,
      expiresText: event.expiresText,
    });
    return htmlResponse(page, 200);
  };

  const handler = handle as OneClickUnsubscribeHandler;
  handler.handle = handle;
  return handler;
}

export const createUnsubscribeHandler = createOneClickUnsubscribeHandler;

export function handleOneClickUnsubscribe(
  options: OneClickUnsubscribeHandlerOptions,
): OneClickUnsubscribeHandler;
export function handleOneClickUnsubscribe(
  request: UnsubscribeRequest,
  options: OneClickUnsubscribeHandlerOptions,
): Promise<Response>;
export function handleOneClickUnsubscribe(
  requestOrOptions: UnsubscribeRequest | OneClickUnsubscribeHandlerOptions,
  maybeOptions?: OneClickUnsubscribeHandlerOptions,
): OneClickUnsubscribeHandler | Promise<Response> {
  if (maybeOptions !== undefined) {
    return createOneClickUnsubscribeHandler(maybeOptions)(
      requestOrOptions as UnsubscribeRequest,
    );
  }
  return createOneClickUnsubscribeHandler(
    requestOrOptions as OneClickUnsubscribeHandlerOptions,
  );
}

export const handleUnsubscribe = handleOneClickUnsubscribe;

function normalizeSignedHeaderSet(
  signedHeaders: SignedHeaderSet | undefined,
): Set<string> {
  if (signedHeaders === undefined) {
    return new Set();
  }

  const parts: readonly string[] =
    typeof signedHeaders === "string"
      ? [signedHeaders]
      : Array.from(signedHeaders);

  const normalized = new Set<string>();
  for (const part of parts) {
    for (const name of part.split(/[,:;]/)) {
      const normalizedName = name.trim().toLowerCase();
      if (normalizedName.length > 0) {
        normalized.add(normalizedName);
      }
    }
  }
  return normalized;
}

function isSignedHeaderSet(value: unknown): value is SignedHeaderSet {
  return (
    typeof value === "string" ||
    value instanceof Set ||
    Array.isArray(value)
  );
}

function headerMapValue(
  headers: OutboundHeaders | undefined,
  name: string,
): string | undefined {
  const wanted = name.toLowerCase();

  if (Array.isArray(headers)) {
    for (const header of headers) {
      if (
        header !== null &&
        typeof header === "object" &&
        typeof header.name === "string" &&
        header.name.trim().toLowerCase() === wanted
      ) {
        return Array.isArray(header.value)
          ? header.value.join(", ")
          : String(header.value);
      }
    }
    return undefined;
  }

  if (headers === undefined) {
    return undefined;
  }

  for (const [headerName, value] of Object.entries(headers)) {
    if (headerName.trim().toLowerCase() === wanted && value !== undefined) {
      return Array.isArray(value) ? value.join(", ") : String(value);
    }
  }
  return undefined;
}

function signedHeadersFromSignature(signature: string): string | undefined {
  const unfolded = signature.replace(/\r?\n[ \t]+/g, " ");
  for (const tag of unfolded.split(";")) {
    const equals = tag.indexOf("=");
    if (equals === -1) {
      continue;
    }
    if (tag.slice(0, equals).trim().toLowerCase() === "h") {
      return tag.slice(equals + 1).trim();
    }
  }
  return undefined;
}

function extractSignedHeaders(
  email: OutboundEmailInput,
): SignedHeaderSet | undefined {
  const dkim = email.dkim;

  if (isSignedHeaderSet(dkim)) {
    return dkim;
  }

  if (dkim !== null && typeof dkim === "object") {
    const fromDkim = firstDefined(dkim.signedHeaders, dkim.headers, dkim.h);
    if (fromDkim !== undefined) {
      return fromDkim;
    }
    const signature = firstDefined(dkim.signature, dkim.header);
    if (signature !== undefined) {
      return signedHeadersFromSignature(signature);
    }
  }

  const explicit = firstDefined(email.dkimSignedHeaders, email.signedHeaders);
  if (explicit !== undefined) {
    return explicit;
  }

  const signature = firstDefined(
    email.dkimSignature,
    headerMapValue(email.headers, "DKIM-Signature"),
  );
  return signature === undefined
    ? undefined
    : signedHeadersFromSignature(signature);
}

function missingDkimHeaders(
  signedHeaders: SignedHeaderSet | undefined,
): string[] {
  const covered = normalizeSignedHeaderSet(signedHeaders);
  return REQUIRED_UNSUBSCRIBE_DKIM_HEADERS.filter(
    (header) => !covered.has(header.toLowerCase()),
  );
}

export function assertDkimCoversUnsubscribeHeaders(
  signedHeaders: SignedHeaderSet,
): void;
export function assertDkimCoversUnsubscribeHeaders(
  email: OutboundEmailInput,
  signedHeaders?: SignedHeaderSet,
): void;
export function assertDkimCoversUnsubscribeHeaders(
  emailOrSignedHeaders: OutboundEmailInput | SignedHeaderSet,
  signedHeaders?: SignedHeaderSet,
): void {
  const set =
    isSignedHeaderSet(emailOrSignedHeaders)
      ? signedHeaders ?? emailOrSignedHeaders
      : signedHeaders ?? extractSignedHeaders(emailOrSignedHeaders);

  const missing = missingDkimHeaders(set);
  if (missing.length > 0) {
    throw new DkimHeaderCoverageError(missing);
  }
}

export function isDkimCoverageValid(
  email: OutboundEmailInput | SignedHeaderSet,
  signedHeaders?: SignedHeaderSet,
): boolean {
  try {
    if (isSignedHeaderSet(email)) {
      return missingDkimHeaders(email).length === 0;
    }
    return missingDkimHeaders(
      signedHeaders ?? extractSignedHeaders(email),
    ).length === 0;
  } catch {
    return false;
  }
}

export function buildOutboundEmail(
  input: OutboundEmailInput,
  signedHeaders?: SignedHeaderSet,
): OutboundEmailInput {
  if (input === null || typeof input !== "object" || Array.isArray(input)) {
    throw new TypeError("outbound email must be an object");
  }

  assertDkimCoversUnsubscribeHeaders(input, signedHeaders);

  return Object.freeze({ ...input });
}

export const createOutboundEmail = buildOutboundEmail;
export const buildEmail = buildOutboundEmail;
export const createEmail = buildOutboundEmail;
export const prepareOutboundEmail = buildOutboundEmail;
export const assertDkimHeaders = assertDkimCoversUnsubscribeHeaders;
export const validateDkimHeaders = assertDkimCoversUnsubscribeHeaders;
export const assertDkimSignedHeaders = assertDkimCoversUnsubscribeHeaders;
export const validateDkimSignedHeaders = assertDkimCoversUnsubscribeHeaders;

export const generateUnsubscribeToken = createUnsubscribeToken;
export const signUnsubscribeToken = createUnsubscribeToken;
export const validateUnsubscribeToken = verifyUnsubscribeToken;
export const buildUnsubscribeUrl = createOneClickUnsubscribeUrl;
export const createUnsubscribeUrl = createOneClickUnsubscribeUrl;
