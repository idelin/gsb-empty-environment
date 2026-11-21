import { randomBytes, createHash } from 'node:crypto';

export type SessionStatus = 'active' | 'rotated' | 'expired';

export type SessionErrorCode =
  | 'SESSION_NOT_FOUND'
  | 'SESSION_ROTATED'
  | 'SESSION_EXPIRED'
  | 'REPLAY_TOKEN_REUSED';

export class SessionError extends Error {
  readonly code: SessionErrorCode;
  readonly httpStatus: number;
  /** Present on SESSION_ROTATED so the stale tab can locate the successor. */
  readonly rotatedTo?: string;

  constructor(code: SessionErrorCode, httpStatus: number, message: string, rotatedTo?: string) {
    super(message);
    this.name = 'SessionError';
    this.code = code;
    this.httpStatus = httpStatus;
    this.rotatedTo = rotatedTo;
  }
}

export interface Session {
  readonly id: string;
  status: SessionStatus;
  /** Context that survives rotation (locale, cart id, csrf seed, ...). */
  readonly persistentContext: Record<string, unknown>;
  /** Privilege-derived data. NEVER carried across rotation. */
  readonly permissionCache: Map<string, unknown>;
  /** Ledger of consumed anti-replay tokens; migrated on rotation. */
  readonly usedReplayTokens: Set<string>;
  readonly createdAt: number;
  rotatedTo?: string;
}

export interface RotateOptions {
  /** Extra context to merge into the new session (e.g. authenticated userId). */
  context?: Record<string, unknown>;
}

const generateId = (): string => randomBytes(32).toString('base64url');

const hashToken = (token: string): string =>
  createHash('sha256').update(token).digest('hex');

export class SessionService {
  private readonly sessions = new Map<string, Session>();

  createSession(context: Record<string, unknown> = {}): Session {
    const session: Session = {
      id: generateId(),
      status: 'active',
      persistentContext: { ...context },
      permissionCache: new Map(),
      usedReplayTokens: new Set(),
      createdAt: Date.now(),
    };
    this.sessions.set(session.id, session);
    return session;
  }

  /**
   * Resolve a session for an incoming request. Stale tabs holding a rotated
   * id get a recognizable SESSION_ROTATED rejection instead of a generic
   * not-found, so clients can distinguish "log in again" from "re-sync".
   */
  resolve(sessionId: string): Session {
    const session = this.sessions.get(sessionId);
    if (!session) {
      throw new SessionError('SESSION_NOT_FOUND', 401, 'Unknown session id');
    }
    if (session.status === 'rotated') {
      throw new SessionError(
        'SESSION_ROTATED',
        409,
        'Session was rotated; retry with the successor session',
        session.rotatedTo,
      );
    }
    if (session.status === 'expired') {
      throw new SessionError('SESSION_EXPIRED', 401, 'Session expired');
    }
    return session;
  }

  /**
   * Issue a brand-new session id after authentication, invalidate the old
   * one, carry over persistent context and the anti-replay ledger, and drop
   * the permission cache. Synchronous so concurrent rotations cannot both
   * succeed: the loser sees SESSION_ROTATED with the successor id.
   */
  rotate(oldSessionId: string, options: RotateOptions = {}): Session {
    const old = this.resolve(oldSessionId);

    const next: Session = {
      id: generateId(),
      status: 'active',
      persistentContext: { ...old.persistentContext, ...options.context },
      permissionCache: new Map(),
      usedReplayTokens: new Set(old.usedReplayTokens),
      createdAt: Date.now(),
    };

    old.status = 'rotated';
    old.rotatedTo = next.id;
    old.permissionCache.clear();

    this.sessions.set(next.id, next);
    return next;
  }

  /**
   * Consume an anti-replay token. First use wins; any reuse — including
   * after rotation, since the ledger migrates — is rejected.
   */
  consumeReplayToken(sessionId: string, token: string): void {
    const session = this.resolve(sessionId);
    const digest = hashToken(token);
    if (session.usedReplayTokens.has(digest)) {
      throw new SessionError('REPLAY_TOKEN_REUSED', 409, 'Anti-replay token already used');
    }
    session.usedReplayTokens.add(digest);
  }

  hasConsumedReplayToken(sessionId: string, token: string): boolean {
    const session = this.sessions.get(sessionId);
    return session?.usedReplayTokens.has(hashToken(token)) ?? false;
  }

  cachePermission(sessionId: string, key: string, value: unknown): void {
    this.resolve(sessionId).permissionCache.set(key, value);
  }

  getCachedPermission(sessionId: string, key: string): unknown {
    return this.resolve(sessionId).permissionCache.get(key);
  }
}
