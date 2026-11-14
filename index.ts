import { createHash, randomBytes } from "node:crypto";

export type ConcurrentLoginPolicy = "single" | "allow" | "limit";

export type SessionInvalidReason =
  | "missing_session_id"
  | "malformed_session_id"
  | "unknown_session_id"
  | "expired"
  | "device_mismatch"
  | "permission_generation_mismatch"
  | "credential_revoked";

export interface StoredSession {
  readonly sessionId: string;
  readonly userId: string;
  readonly authenticatedAt: number;
  readonly createdAt: number;
  readonly lastSeenAt: number;
  readonly idleExpiresAt: number;
  readonly absoluteExpiresAt: number;
  readonly expiresAt: number;
  readonly deviceDigest: string;
  readonly permissionGeneration: number;
  readonly credentialGeneration: number;
  readonly metadata: Readonly<Record<string, unknown>>;
}

/**
 * The service only reads sessions from this store. An implementation backed
 * by a database should key every operation by the server-generated opaque ID.
 */
export interface SessionStore {
  get(sessionId: string): StoredSession | undefined;
  set(session: StoredSession): void;
  delete(sessionId: string): boolean;
  deleteByUser(userId: string): number;
  listByUser(userId: string): StoredSession[];
}

export class InMemorySessionStore implements SessionStore {
  private readonly sessions = new Map<string, StoredSession>();

  constructor(initialSessions: Iterable<StoredSession> = []) {
    for (const session of initialSessions) {
      this.set(session);
    }
  }

  get sessionCount(): number {
    return this.sessions.size;
  }

  get(sessionId: string): StoredSession | undefined {
    return this.sessions.get(sessionId);
  }

  set(session: StoredSession): void {
    this.sessions.set(session.sessionId, session);
  }

  delete(sessionId: string): boolean {
    return this.sessions.delete(sessionId);
  }

  deleteByUser(userId: string): number {
    let deleted = 0;
    for (const [sessionId, session] of this.sessions) {
      if (session.userId === userId) {
        this.sessions.delete(sessionId);
        deleted += 1;
      }
    }
    return deleted;
  }

  listByUser(userId: string): StoredSession[] {
    const matches: StoredSession[] = [];
    for (const session of this.sessions.values()) {
      if (session.userId === userId) {
        matches.push(session);
      }
    }
    return matches;
  }
}

export interface LoginSessionOptions {
  store?: SessionStore;
  clock?: () => number;
  idGenerator?: () => string;
  idleTimeoutMs?: number;
  absoluteTimeoutMs?: number;
  concurrentLoginPolicy?: ConcurrentLoginPolicy;
  maxSessionsPerUser?: number;
  rotateOnRenew?: boolean;
}

export interface LoginInput {
  userId: string;
  deviceDigest: string;
  permissionGeneration?: number;
  presentedSessionId?: string | null;
  metadata?: Readonly<Record<string, unknown>>;
}

export interface SessionRequest {
  sessionId?: string | null;
  deviceDigest?: string;
  [clientClaim: string]: unknown;
}

export interface LoginResult {
  readonly sessionId: string;
  readonly session: StoredSession;
  readonly rotatedFrom: string | null;
}

export interface SessionInspection {
  readonly authenticated: boolean;
  readonly reason: SessionInvalidReason | null;
  readonly session: StoredSession | null;
}

interface UserSecurityState {
  permissionGeneration: number;
  credentialGeneration: number;
}

const DEFAULT_IDLE_TIMEOUT_MS = 30 * 60 * 1000;
const DEFAULT_ABSOLUTE_TIMEOUT_MS = 12 * 60 * 60 * 1000;
const DEFAULT_MAX_SESSIONS_PER_USER = 3;
const SESSION_ID_PATTERN = /^[A-Za-z0-9_-]+$/;
const MAX_SESSION_ID_LENGTH = 512;

function assertPositiveInteger(value: number, name: string): void {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`${name} must be a positive safe integer`);
  }
}

function assertGeneration(value: number, name: string): void {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new TypeError(`${name} must be a non-negative safe integer`);
  }
}

function normalizeSessionId(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function isWellFormedSessionId(value: string): boolean {
  return value.length <= MAX_SESSION_ID_LENGTH && SESSION_ID_PATTERN.test(value);
}

function freezeMetadata(
  metadata: Readonly<Record<string, unknown>> | undefined,
): Readonly<Record<string, unknown>> {
  return Object.freeze({ ...(metadata ?? {}) });
}

function normalizeMetadata(
  metadata: Readonly<Record<string, unknown>> | undefined,
): Readonly<Record<string, unknown>> {
  if (metadata === undefined) {
    return freezeMetadata(undefined);
  }
  if (
    metadata === null ||
    typeof metadata !== "object" ||
    Array.isArray(metadata)
  ) {
    throw new TypeError("metadata must be a plain object");
  }
  return freezeMetadata(metadata);
}

function cloneSession(session: StoredSession): StoredSession {
  return Object.freeze({
    ...session,
    metadata: freezeMetadata(session.metadata),
  });
}

function extractSessionId(
  request: SessionRequest | string | null | undefined,
): string | undefined {
  if (typeof request === "string") {
    return request;
  }
  if (request === null || request === undefined) {
    return undefined;
  }
  return normalizeSessionId(
    request.sessionId ?? request.sessionID ?? request.id ?? request.cookie,
  );
}

function extractDeviceDigest(
  request: SessionRequest | string | null | undefined,
  explicitDeviceDigest: string | undefined,
): string | undefined {
  if (explicitDeviceDigest !== undefined) {
    return normalizeSessionId(explicitDeviceDigest);
  }
  if (request !== null && typeof request === "object") {
    return normalizeSessionId(request.deviceDigest);
  }
  return undefined;
}

/**
 * Build a digest from server-collected device signals. Do not use a value
 * supplied directly by an untrusted client as the digest.
 */
export function digestDevice(
  ...deviceSignals: readonly (string | readonly string[])[]
): string {
  const flattened: string[] = [];
  for (const signal of deviceSignals) {
    if (Array.isArray(signal)) {
      for (const part of signal) {
        flattened.push(part);
      }
    } else {
      flattened.push(signal);
    }
  }
  return createHash("sha256")
    .update(flattened.join("\u0000"), "utf8")
    .digest("base64url");
}

export const deviceDigest = digestDevice;

export class LoginSessionService {
  readonly store: SessionStore;
  readonly idleTimeoutMs: number;
  readonly absoluteTimeoutMs: number;
  readonly concurrentLoginPolicy: ConcurrentLoginPolicy;
  readonly maxSessionsPerUser: number;
  readonly rotateOnRenew: boolean;
  private readonly clock: () => number;
  private readonly idGenerator: () => string;
  private readonly users = new Map<string, UserSecurityState>();

  constructor(options: LoginSessionOptions = {}) {
    const idleTimeoutMs = options.idleTimeoutMs ?? DEFAULT_IDLE_TIMEOUT_MS;
    const absoluteTimeoutMs =
      options.absoluteTimeoutMs ?? DEFAULT_ABSOLUTE_TIMEOUT_MS;
    const maxSessionsPerUser =
      options.maxSessionsPerUser ?? DEFAULT_MAX_SESSIONS_PER_USER;

    assertPositiveInteger(idleTimeoutMs, "idleTimeoutMs");
    assertPositiveInteger(absoluteTimeoutMs, "absoluteTimeoutMs");
    assertPositiveInteger(maxSessionsPerUser, "maxSessionsPerUser");

    this.store = options.store ?? new InMemorySessionStore();
    this.clock = options.clock ?? Date.now;
    this.idGenerator =
      options.idGenerator ?? (() => randomBytes(32).toString("base64url"));
    this.idleTimeoutMs = idleTimeoutMs;
    this.absoluteTimeoutMs = absoluteTimeoutMs;
    this.concurrentLoginPolicy = options.concurrentLoginPolicy ?? "single";
    this.maxSessionsPerUser = maxSessionsPerUser;
    this.rotateOnRenew = options.rotateOnRenew ?? false;
  }

  /**
   * Complete an authentication that has already been verified by the caller.
   * The returned ID is the only valid ID: the presented pre-authentication ID
   * is never reused, and existing sessions are handled by the configured
   * concurrent-login policy.
   */
  login(input: LoginInput): LoginResult {
    if (input === null || typeof input !== "object") {
      throw new TypeError("login input is required");
    }

    const userId = normalizeSessionId(input.userId);
    const deviceDigestValue = normalizeSessionId(input.deviceDigest);
    if (userId === undefined) {
      throw new TypeError("userId is required");
    }
    if (deviceDigestValue === undefined) {
      throw new TypeError("deviceDigest is required");
    }

    const permissionGeneration = input.permissionGeneration ?? null;
    if (permissionGeneration !== null) {
      assertGeneration(permissionGeneration, "permissionGeneration");
    }
    const metadata = normalizeMetadata(input.metadata);
    const now = this.now();
    const presentedSessionId = normalizeSessionId(input.presentedSessionId);

    let rotatedFrom: string | null = null;
    if (presentedSessionId !== undefined) {
      const presented = this.store.get(presentedSessionId);
      if (presented !== undefined) {
        this.store.delete(presentedSessionId);
        rotatedFrom = presentedSessionId;
      }
    }

    const userState = this.ensureUserState(
      userId,
      permissionGeneration ?? undefined,
    );
    if (permissionGeneration !== null) {
      if (permissionGeneration < userState.permissionGeneration) {
        throw new PermissionGenerationConflictError(
          userId,
          userState.permissionGeneration,
          permissionGeneration,
        );
      }
      if (permissionGeneration > userState.permissionGeneration) {
        userState.permissionGeneration = permissionGeneration;
        this.store.deleteByUser(userId);
      }
    }

    this.applyConcurrentLoginPolicy(userId, now);

    const sessionId = this.generateSessionId(
      presentedSessionId === undefined
        ? undefined
        : new Set<string>([presentedSessionId]),
    );
    const absoluteExpiresAt = now + this.absoluteTimeoutMs;
    const idleExpiresAt = now + this.idleTimeoutMs;
    const session: StoredSession = Object.freeze({
      sessionId,
      userId,
      authenticatedAt: now,
      createdAt: now,
      lastSeenAt: now,
      idleExpiresAt,
      absoluteExpiresAt,
      expiresAt: Math.min(idleExpiresAt, absoluteExpiresAt),
      deviceDigest: deviceDigestValue,
      permissionGeneration: userState.permissionGeneration,
      credentialGeneration: userState.credentialGeneration,
      metadata,
    });
    this.store.set(session);

    return Object.freeze({
      sessionId,
      session: cloneSession(session),
      rotatedFrom,
    });
  }

  /**
   * Alias for login() for callers that treat login as the session creation
   * boundary. Credentials must be checked before this method is called.
   */
  createSession(input: LoginInput): LoginResult {
    return this.login(input);
  }

  /**
   * Resolve an authenticated session by the server-side store. Client claims
   * such as userId or permissionGeneration in the request are intentionally
   * ignored.
   */
  inspect(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): SessionInspection {
    const sessionId = extractSessionId(request);
    if (sessionId === undefined) {
      return {
        authenticated: false,
        reason: "missing_session_id",
        session: null,
      };
    }
    if (!isWellFormedSessionId(sessionId)) {
      return {
        authenticated: false,
        reason: "malformed_session_id",
        session: null,
      };
    }

    const stored = this.store.get(sessionId);
    if (stored === undefined) {
      return {
        authenticated: false,
        reason: "unknown_session_id",
        session: null,
      };
    }

    const now = this.now();
    if (now >= stored.expiresAt) {
      this.store.delete(sessionId);
      return {
        authenticated: false,
        reason: "expired",
        session: null,
      };
    }

    const deviceDigestValue = extractDeviceDigest(
      request,
      explicitDeviceDigest,
    );
    if (
      deviceDigestValue !== undefined &&
      deviceDigestValue !== stored.deviceDigest
    ) {
      return {
        authenticated: false,
        reason: "device_mismatch",
        session: null,
      };
    }

    const userState = this.users.get(stored.userId);
    if (
      userState !== undefined &&
      stored.permissionGeneration !== userState.permissionGeneration
    ) {
      this.store.delete(sessionId);
      return {
        authenticated: false,
        reason: "permission_generation_mismatch",
        session: null,
      };
    }
    if (
      userState !== undefined &&
      stored.credentialGeneration !== userState.credentialGeneration
    ) {
      this.store.delete(sessionId);
      return {
        authenticated: false,
        reason: "credential_revoked",
        session: null,
      };
    }

    return {
      authenticated: true,
      reason: null,
      session: cloneSession(stored),
    };
  }

  authenticate(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): StoredSession | null {
    const inspection = this.inspect(request, explicitDeviceDigest);
    return inspection.authenticated ? inspection.session : null;
  }

  validate(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): StoredSession | null {
    return this.authenticate(request, explicitDeviceDigest);
  }

  getSession(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): StoredSession | null {
    return this.authenticate(request, explicitDeviceDigest);
  }

  /**
   * A valid renewal extends the idle expiration but never changes the
   * authentication time or grants a new permission generation. Set
   * rotateOnRenew when the application also wants an ID rotation here.
   */
  renew(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): StoredSession | null {
    const inspection = this.inspect(request, explicitDeviceDigest);
    if (!inspection.authenticated || inspection.session === null) {
      return null;
    }

    const now = this.now();
    const current = inspection.session;
    if (now >= current.absoluteExpiresAt) {
      this.store.delete(current.sessionId);
      return null;
    }

    const idleExpiresAt = now + this.idleTimeoutMs;
    const expiresAt = Math.min(idleExpiresAt, current.absoluteExpiresAt);
    const renewed: StoredSession = Object.freeze({
      ...current,
      lastSeenAt: now,
      idleExpiresAt,
      expiresAt,
    });

    if (this.rotateOnRenew) {
      const rotated = this.replaceSession(renewed);
      return rotated === null ? null : rotated.session;
    }

    this.store.set(renewed);
    return cloneSession(renewed);
  }

  refresh(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): StoredSession | null {
    return this.renew(request, explicitDeviceDigest);
  }

  /**
   * Issue a fresh ID while preserving the authenticated identity and time.
   * The old ID is deleted before the new record is returned.
   */
  rotate(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): LoginResult | null {
    const inspection = this.inspect(request, explicitDeviceDigest);
    if (!inspection.authenticated || inspection.session === null) {
      return null;
    }
    return this.replaceSession(inspection.session);
  }

  /**
   * Logout deletes the server-side record. A supplied device digest must
   * match; an unknown or already-revoked ID simply returns false.
   */
  logout(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): boolean {
    const sessionId = extractSessionId(request);
    if (sessionId === undefined || !isWellFormedSessionId(sessionId)) {
      return false;
    }
    const stored = this.store.get(sessionId);
    if (stored === undefined) {
      return false;
    }

    const deviceDigestValue = extractDeviceDigest(
      request,
      explicitDeviceDigest,
    );
    if (
      deviceDigestValue !== undefined &&
      deviceDigestValue !== stored.deviceDigest
    ) {
      return false;
    }

    return this.store.delete(sessionId);
  }

  destroySession(
    request: SessionRequest | string | null | undefined,
    explicitDeviceDigest?: string,
  ): boolean {
    return this.logout(request, explicitDeviceDigest);
  }

  /**
   * Permission changes revoke every session for the user and advance the
   * generation unless an explicit, non-decreasing generation is supplied.
   * Returns the number of revoked sessions.
   */
  changePermissions(userId: string, permissionGeneration?: number): number {
    const normalizedUserId = normalizeSessionId(userId);
    if (normalizedUserId === undefined) {
      throw new TypeError("userId is required");
    }
    const state = this.ensureUserState(normalizedUserId);
    let nextGeneration: number;
    if (permissionGeneration === undefined) {
      nextGeneration = state.permissionGeneration + 1;
    } else {
      assertGeneration(permissionGeneration, "permissionGeneration");
      if (permissionGeneration < state.permissionGeneration) {
        throw new PermissionGenerationConflictError(
          normalizedUserId,
          state.permissionGeneration,
          permissionGeneration,
        );
      }
      nextGeneration = permissionGeneration;
    }
    state.permissionGeneration = nextGeneration;
    return this.store.deleteByUser(normalizedUserId);
  }

  elevatePermissions(userId: string, permissionGeneration?: number): number {
    return this.changePermissions(userId, permissionGeneration);
  }

  setPermissionGeneration(
    userId: string,
    permissionGeneration: number,
  ): number {
    return this.changePermissions(userId, permissionGeneration);
  }

  advancePermissionGeneration(userId: string): number {
    return this.changePermissions(userId);
  }

  /**
   * Password reset advances the credential generation and immediately
   * revokes all sessions. A later login receives the new generation.
   */
  resetPassword(userId: string): number {
    const normalizedUserId = normalizeSessionId(userId);
    if (normalizedUserId === undefined) {
      throw new TypeError("userId is required");
    }
    const state = this.ensureUserState(normalizedUserId);
    state.credentialGeneration += 1;
    return this.store.deleteByUser(normalizedUserId);
  }

  onPasswordReset(userId: string): number {
    return this.resetPassword(userId);
  }

  revokeUserSessions(userId: string): number {
    const normalizedUserId = normalizeSessionId(userId);
    if (normalizedUserId === undefined) {
      throw new TypeError("userId is required");
    }
    return this.store.deleteByUser(normalizedUserId);
  }

  getPermissionGeneration(userId: string): number {
    return this.ensureUserState(userId).permissionGeneration;
  }

  private now(): number {
    const value = this.clock();
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new TypeError("clock must return a non-negative integer");
    }
    return value;
  }

  private ensureUserState(
    userId: string,
    initialPermissionGeneration?: number,
  ): UserSecurityState {
    let state = this.users.get(userId);
    if (state === undefined) {
      state = {
        permissionGeneration: initialPermissionGeneration ?? 0,
        credentialGeneration: 0,
      };
      this.users.set(userId, state);
    }
    return state;
  }

  private applyConcurrentLoginPolicy(userId: string, now: number): void {
    if (this.concurrentLoginPolicy === "allow") {
      return;
    }
    if (this.concurrentLoginPolicy === "single") {
      this.store.deleteByUser(userId);
      return;
    }

    const userSessions = this.store.listByUser(userId);
    const activeSessions = userSessions
      .filter((session) => session.expiresAt > now)
      .sort((left, right) => left.createdAt - right.createdAt);
    for (const session of userSessions) {
      if (session.expiresAt <= now) {
        this.store.delete(session.sessionId);
      }
    }

    while (activeSessions.length >= this.maxSessionsPerUser) {
      const oldest = activeSessions.shift();
      if (oldest !== undefined) {
        this.store.delete(oldest.sessionId);
      }
    }
  }

  private generateSessionId(excluded: Set<string> | undefined): string {
    const reserved = excluded ?? new Set<string>();
    for (let attempt = 0; attempt < 100; attempt += 1) {
      const candidate = this.idGenerator();
      if (
        typeof candidate === "string" &&
        isWellFormedSessionId(candidate) &&
        !reserved.has(candidate) &&
        this.store.get(candidate) === undefined
      ) {
        return candidate;
      }
    }
    throw new Error("unable to generate a unique session ID");
  }

  private replaceSession(current: StoredSession): LoginResult | null {
    const now = this.now();
    const absoluteExpiresAt = current.absoluteExpiresAt;
    if (now >= absoluteExpiresAt) {
      this.store.delete(current.sessionId);
      return null;
    }

    const sessionId = this.generateSessionId(
      new Set<string>([current.sessionId]),
    );
    const idleExpiresAt = Math.min(
      now + this.idleTimeoutMs,
      absoluteExpiresAt,
    );
    const replacement: StoredSession = Object.freeze({
      ...current,
      sessionId,
      lastSeenAt: now,
      idleExpiresAt,
      expiresAt: Math.min(idleExpiresAt, absoluteExpiresAt),
      metadata: freezeMetadata(current.metadata),
    });

    this.store.delete(current.sessionId);
    this.store.set(replacement);
    return Object.freeze({
      sessionId,
      session: cloneSession(replacement),
      rotatedFrom: current.sessionId,
    });
  }
}

export class PermissionGenerationConflictError extends Error {
  readonly userId: string;
  readonly expected: number;
  readonly received: number;

  constructor(userId: string, expected: number, received: number) {
    super(
      `permission generation for ${userId} is ${expected}; received ${received}`,
    );
    this.name = "PermissionGenerationConflictError";
    this.userId = userId;
    this.expected = expected;
    this.received = received;
  }
}

export { LoginSessionService as SessionService };
export { LoginSessionService as SessionManager };

export function createSessionService(
  options: LoginSessionOptions = {},
): LoginSessionService {
  return new LoginSessionService(options);
}

export default LoginSessionService;
