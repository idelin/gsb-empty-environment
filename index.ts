export const LOGIN_LINK_TTL_MS = 15 * 60 * 1000;
export const ISSUANCE_WINDOW_MS = 10 * 60 * 1000;
export const MAX_ISSUANCES_PER_EMAIL = 5;
export const DEVICE_BINDING_BYTES = 16;
export const LOGIN_TOKEN_BYTES = 32;
export const DEVICE_BINDING_COOKIE_NAME = "__Host-device_binding";
export const SESSION_COOKIE_NAME = "__Host-session";

export type LoginLinkFailureCode =
  | "invalid_email"
  | "rate_limited"
  | "device_binding_required"
  | "invalid_or_expired_token";

export interface LoginLinkFailure {
  ok: false;
  code: LoginLinkFailureCode;
  status: number;
  message: string;
  fallback?: "email_code";
  retryAfterMs?: number;
}

export interface LoginLinkIssuance {
  ok: true;
  email: string;
  token: string;
  expiresAt: number;
  deviceBinding: string;
  cookieName: string;
  cookie: string;
  setCookie: string;
}

export interface DeviceBindingCookieOptions {
  sameSite?: "Strict" | "Lax" | "None";
  maxAgeSeconds?: number;
}

export interface SessionCookieOptions {
  sameSite?: "Strict" | "Lax" | "None";
  maxAgeSeconds?: number;
}

export interface LoginLinkServiceOptions {
  now?: () => number;
  randomBytes?: (byteLength: number) => Uint8Array;
  deviceBindingCookieName?: string;
  sessionCookieName?: string;
  deviceBindingCookie?: DeviceBindingCookieOptions;
  sessionCookie?: SessionCookieOptions;
}

export interface IssueLoginLinkOptions {
  deviceBinding?: string;
}

export interface RedeemInput {
  token: string;
  deviceBinding?: string;
  currentSessionId?: string;
}

export interface RedeemedSession {
  id: string;
  previousSessionId: string | null;
  email: string;
  createdAt: number;
  setCookie: string;
}

export interface RedeemSuccess {
  ok: true;
  consumed: true;
  email: string;
  sessionId: string;
  previousSessionId: string | null;
  rotated: true;
  sessionCookie: string;
  sessionSetCookie: string;
  session: RedeemedSession;
}

export interface RedeemFailure extends LoginLinkFailure {
  consumed: false;
}

export type RedeemResult = RedeemSuccess | RedeemFailure;

export interface LoginLinkHttpResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

interface StoredLink {
  email: string;
  tokenHash: string;
  deviceHash: string;
  issuedAt: number;
  expiresAt: number;
}

interface StoredSession {
  email: string;
  createdAt: number;
  rotatedFrom: string | null;
}

function webCrypto(): Crypto {
  const value = globalThis.crypto;
  if (!value || !value.subtle) {
    throw new Error("Web Crypto is required to issue and redeem login links");
  }
  return value;
}

function randomValue(byteLength: number): Uint8Array {
  const bytes = new Uint8Array(byteLength);
  webCrypto().getRandomValues(bytes);
  return bytes;
}

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/u, "");
}

async function sha256(value: string): Promise<string> {
  const digest = await webCrypto().subtle.digest(
    "SHA-256",
    new TextEncoder().encode(value),
  );
  const bytes = new Uint8Array(digest);
  let hex = "";
  for (const byte of bytes) {
    hex += byte.toString(16).padStart(2, "0");
  }
  return hex;
}

function constantTimeEqual(left: string, right: string): boolean {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  let difference = leftBytes.byteLength ^ rightBytes.byteLength;
  const length = Math.max(leftBytes.byteLength, rightBytes.byteLength);
  for (let index = 0; index < length; index += 1) {
    difference |= (leftBytes[index] ?? 0) ^ (rightBytes[index] ?? 0);
  }
  return difference === 0;
}

function normalizeEmail(email: string): string | null {
  if (typeof email !== "string") {
    return null;
  }
  const normalized = email.trim().normalize("NFKC").toLowerCase();
  if (
    normalized.length === 0 ||
    normalized.length > 254 ||
    !/^[^\s@]+@[^\s@]+\.[^\s@]+$/u.test(normalized)
  ) {
    return null;
  }
  return normalized;
}

function isDeviceBinding(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9_-]{22}$/u.test(value);
}

function isLoginToken(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9_-]{43}$/u.test(value);
}

function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function invalidEmailFailure(): LoginLinkFailure {
  return {
    ok: false,
    code: "invalid_email",
    status: 400,
    message: "请输入有效的邮箱地址。",
  };
}

function bindingFailure(): RedeemFailure {
  return {
    ok: false,
    consumed: false,
    code: "device_binding_required",
    status: 400,
    message: "设备绑定缺失或不匹配，请改用邮件里的验证码。",
    fallback: "email_code",
  };
}

function invalidTokenFailure(): RedeemFailure {
  return {
    ok: false,
    consumed: false,
    code: "invalid_or_expired_token",
    status: 400,
    message: "登录链接无效或已过期，请重新获取登录链接。",
  };
}

function cookieHeader(
  name: string,
  value: string,
  maxAgeSeconds: number,
  sameSite: DeviceBindingCookieOptions["sameSite"],
): string {
  return [
    `${name}=${value}`,
    "Path=/",
    "HttpOnly",
    "Secure",
    `SameSite=${sameSite ?? "Lax"}`,
    `Max-Age=${maxAgeSeconds}`,
  ].join("; ");
}

function parseCookies(header: string | undefined): Map<string, string> {
  const cookies = new Map<string, string>();
  if (!header) {
    return cookies;
  }
  for (const part of header.split(";")) {
    const separator = part.indexOf("=");
    if (separator < 0) {
      continue;
    }
    const name = part.slice(0, separator).trim();
    const value = part.slice(separator + 1).trim();
    if (name.length > 0 && !cookies.has(name)) {
      cookies.set(name, value);
    }
  }
  return cookies;
}

export function getDeviceBindingFromCookie(
  cookieHeaderValue: string | undefined,
  cookieName: string = DEVICE_BINDING_COOKIE_NAME,
): string | undefined {
  return parseCookies(cookieHeaderValue).get(cookieName);
}

export class LoginLinkService {
  private readonly now: () => number;
  private readonly randomBytes: (byteLength: number) => Uint8Array;
  private readonly deviceCookieName: string;
  private readonly sessionCookieName: string;
  private readonly deviceCookieMaxAgeSeconds: number;
  private readonly deviceCookieSameSite: DeviceBindingCookieOptions["sameSite"];
  private readonly sessionCookieMaxAgeSeconds: number;
  private readonly sessionCookieSameSite: SessionCookieOptions["sameSite"];
  private readonly linksByTokenHash = new Map<string, StoredLink>();
  private readonly latestLinkByEmail = new Map<string, StoredLink>();
  private readonly issuanceTimesByEmail = new Map<string, number[]>();
  private readonly issuanceSequenceByEmail = new Map<string, number>();
  private readonly sessions = new Map<string, StoredSession>();
  private sequence = 0;

  constructor(options: LoginLinkServiceOptions = {}) {
    this.now = options.now ?? (() => Date.now());
    this.randomBytes = options.randomBytes ?? randomValue;
    this.deviceCookieName =
      options.deviceBindingCookieName ?? DEVICE_BINDING_COOKIE_NAME;
    this.sessionCookieName =
      options.sessionCookieName ?? SESSION_COOKIE_NAME;
    this.deviceCookieMaxAgeSeconds =
      options.deviceBindingCookie?.maxAgeSeconds ?? LOGIN_LINK_TTL_MS / 1000;
    this.deviceCookieSameSite =
      options.deviceBindingCookie?.sameSite ?? "Lax";
    this.sessionCookieMaxAgeSeconds =
      options.sessionCookie?.maxAgeSeconds ?? 8 * 60 * 60;
    this.sessionCookieSameSite = options.sessionCookie?.sameSite ?? "Lax";
  }

  async issueLoginLink(
    email: string,
    options: IssueLoginLinkOptions = {},
  ): Promise<LoginLinkIssuance | LoginLinkFailure> {
    const normalizedEmail = normalizeEmail(email);
    if (!normalizedEmail) {
      return invalidEmailFailure();
    }

    const requestedBinding = options.deviceBinding;
    if (requestedBinding !== undefined && !isDeviceBinding(requestedBinding)) {
      return bindingFailure();
    }

    const deviceBinding =
      requestedBinding ??
      bytesToBase64Url(this.randomBytes(DEVICE_BINDING_BYTES));
    const token = bytesToBase64Url(this.randomBytes(LOGIN_TOKEN_BYTES));
    const now = this.now();
    this.pruneIssuanceTimes(normalizedEmail, now);

    const issuanceTimes = this.issuanceTimesByEmail.get(normalizedEmail) ?? [];
    if (issuanceTimes.length >= MAX_ISSUANCES_PER_EMAIL) {
      const oldest = issuanceTimes[0] ?? now;
      return {
        ok: false,
        code: "rate_limited",
        status: 429,
        message: "登录链接签发过于频繁，请稍后再试。",
        retryAfterMs: Math.max(0, oldest + ISSUANCE_WINDOW_MS - now),
      };
    }

    issuanceTimes.push(now);
    this.issuanceTimesByEmail.set(normalizedEmail, issuanceTimes);
    const sequence = ++this.sequence;
    this.issuanceSequenceByEmail.set(normalizedEmail, sequence);

    const [tokenHash, deviceHash] = await Promise.all([
      sha256(token),
      sha256(deviceBinding),
    ]);

    const expiresAt = now + LOGIN_LINK_TTL_MS;
    if (this.issuanceSequenceByEmail.get(normalizedEmail) === sequence) {
      const link: StoredLink = {
        email: normalizedEmail,
        tokenHash,
        deviceHash,
        issuedAt: now,
        expiresAt,
      };
      const previous = this.latestLinkByEmail.get(normalizedEmail);
      if (previous) {
        this.linksByTokenHash.delete(previous.tokenHash);
      }
      this.latestLinkByEmail.set(normalizedEmail, link);
      this.linksByTokenHash.set(tokenHash, link);
    }

    const cookie = cookieHeader(
      this.deviceCookieName,
      deviceBinding,
      this.deviceCookieMaxAgeSeconds,
      this.deviceCookieSameSite,
    );
    return {
      ok: true,
      email: normalizedEmail,
      token,
      expiresAt,
      deviceBinding,
      cookieName: this.deviceCookieName,
      cookie,
      setCookie: cookie,
    };
  }

  async createLoginLink(
    email: string,
    options: IssueLoginLinkOptions = {},
  ): Promise<LoginLinkIssuance | LoginLinkFailure> {
    return this.issueLoginLink(email, options);
  }

  async issue(
    email: string,
    options: IssueLoginLinkOptions = {},
  ): Promise<LoginLinkIssuance | LoginLinkFailure> {
    return this.issueLoginLink(email, options);
  }

  async redeem(
    input: RedeemInput | string,
    deviceBinding?: string,
    currentSessionIdOrOptions?: string | { currentSessionId?: string },
  ): Promise<RedeemResult> {
    const values: RedeemInput =
      typeof input === "string"
        ? {
            token: input,
            deviceBinding,
            currentSessionId:
              typeof currentSessionIdOrOptions === "string"
                ? currentSessionIdOrOptions
                : currentSessionIdOrOptions?.currentSessionId,
          }
        : input;

    if (!isDeviceBinding(values.deviceBinding)) {
      return bindingFailure();
    }
    if (!isLoginToken(values.token)) {
      return invalidTokenFailure();
    }

    const [tokenHash, deviceHash] = await Promise.all([
      sha256(values.token),
      sha256(values.deviceBinding),
    ]);
    const now = this.now();
    const link = this.linksByTokenHash.get(tokenHash);
    if (!link) {
      return invalidTokenFailure();
    }
    if (now >= link.expiresAt) {
      this.linksByTokenHash.delete(tokenHash);
      if (this.latestLinkByEmail.get(link.email)?.tokenHash === tokenHash) {
        this.latestLinkByEmail.delete(link.email);
      }
      return invalidTokenFailure();
    }
    if (!constantTimeEqual(deviceHash, link.deviceHash)) {
      return bindingFailure();
    }

    const sessionId = bytesToBase64Url(this.randomBytes(LOGIN_TOKEN_BYTES));
    const session = this.rotateSession(
      link.email,
      values.currentSessionId,
      sessionId,
      now,
    );
    this.linksByTokenHash.delete(tokenHash);
    if (this.latestLinkByEmail.get(link.email)?.tokenHash === tokenHash) {
      this.latestLinkByEmail.delete(link.email);
    }

    return {
      ok: true,
      consumed: true,
      email: link.email,
      sessionId,
      previousSessionId: session.previousSessionId,
      rotated: true,
      sessionCookie: session.setCookie,
      sessionSetCookie: session.setCookie,
      session,
    };
  }

  async exchange(
    input: RedeemInput | string,
    deviceBinding?: string,
    currentSessionIdOrOptions?: string | { currentSessionId?: string },
  ): Promise<RedeemResult> {
    return this.redeem(input, deviceBinding, currentSessionIdOrOptions);
  }

  async redeemLoginLink(
    input: RedeemInput | string,
    deviceBinding?: string,
    currentSessionIdOrOptions?: string | { currentSessionId?: string },
  ): Promise<RedeemResult> {
    return this.redeem(input, deviceBinding, currentSessionIdOrOptions);
  }

  getConfirmationPage(token: string, action = "/login/verify"): string {
    const safeToken = escapeHtml(token ?? "");
    const safeAction = escapeHtml(action);
    return [
      "<!doctype html>",
      '<html lang="zh-CN">',
      "<head>",
      '<meta charset="utf-8">',
      '<meta name="viewport" content="width=device-width, initial-scale=1">',
      "<title>确认登录</title>",
      "</head>",
      "<body>",
      "<main>",
      "<h1>确认登录</h1>",
      "<p>点击下方按钮完成登录。该操作不会自动消耗你的登录链接。</p>",
      `<form method="post" action="${safeAction}">`,
      `<input type="hidden" name="token" value="${safeToken}">`,
      '<button type="submit">继续登录</button>',
      "</form>",
      "<p>如果这不是你发起的登录，请忽略此页面并改用邮件中的验证码。</p>",
      "</main>",
      "</body>",
      "</html>",
    ].join("");
  }

  handleGet(token: string | null | undefined): LoginLinkHttpResponse {
    return {
      status: 200,
      headers: {
        "Content-Type": "text/html; charset=utf-8",
        "Cache-Control": "no-store",
        Pragma: "no-cache",
        "Referrer-Policy": "no-referrer",
        "Content-Security-Policy":
          "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'",
        "X-Content-Type-Options": "nosniff",
      },
      body: this.getConfirmationPage(token ?? ""),
    };
  }

  async handlePost(input: RedeemInput & {
    cookieHeader?: string;
  }): Promise<LoginLinkHttpResponse> {
    const deviceBinding =
      input.deviceBinding ??
      getDeviceBindingFromCookie(input.cookieHeader, this.deviceCookieName);
    const result = await this.redeem({
      token: input.token,
      deviceBinding,
      currentSessionId: input.currentSessionId,
    });

    if (!result.ok) {
      return {
        status: result.status,
        headers: {
          "Content-Type": "application/json; charset=utf-8",
          "Cache-Control": "no-store",
        },
        body: JSON.stringify(result),
      };
    }

    return {
      status: 200,
      headers: {
        "Content-Type": "application/json; charset=utf-8",
        "Cache-Control": "no-store",
        "Set-Cookie": result.sessionCookie,
      },
      body: JSON.stringify(result),
    };
  }

  async handleRedemptionRequest(input: RedeemInput & {
    cookieHeader?: string;
  }): Promise<LoginLinkHttpResponse> {
    return this.handlePost(input);
  }

  hasActiveSession(sessionId: string): boolean {
    return this.sessions.has(sessionId);
  }

  getSessionEmail(sessionId: string): string | undefined {
    return this.sessions.get(sessionId)?.email;
  }

  revokeSession(sessionId: string): boolean {
    return this.sessions.delete(sessionId);
  }

  private rotateSession(
    email: string,
    previousSessionId: string | undefined,
    newSessionId: string,
    now: number,
  ): RedeemedSession {
    if (previousSessionId) {
      this.sessions.delete(previousSessionId);
    }
    const stored: StoredSession = {
      email,
      createdAt: now,
      rotatedFrom: previousSessionId ?? null,
    };
    this.sessions.set(newSessionId, stored);
    return {
      id: newSessionId,
      previousSessionId: previousSessionId ?? null,
      email,
      createdAt: now,
      setCookie: cookieHeader(
        this.sessionCookieName,
        newSessionId,
        this.sessionCookieMaxAgeSeconds,
        this.sessionCookieSameSite,
      ),
    };
  }

  private pruneIssuanceTimes(email: string, now: number): void {
    const times = this.issuanceTimesByEmail.get(email);
    if (!times) {
      return;
    }
    const retained = times.filter(
      (issuedAt) => now - issuedAt < ISSUANCE_WINDOW_MS,
    );
    if (retained.length === 0) {
      this.issuanceTimesByEmail.delete(email);
    } else {
      this.issuanceTimesByEmail.set(email, retained);
    }
  }
}

export function createLoginLinkService(
  options: LoginLinkServiceOptions = {},
): LoginLinkService {
  return new LoginLinkService(options);
}

export default LoginLinkService;
