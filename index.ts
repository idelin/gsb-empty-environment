import { spawn } from 'node:child_process';
import { randomBytes, timingSafeEqual } from 'node:crypto';

export type CallerKind = 'extension-page' | 'content-script' | 'web-page';

export interface CallerContext {
  kind: CallerKind;
  extensionId?: string;
  origin?: string;
  scriptId?: string;
  sessionToken?: string;
}

export interface HostSpec {
  command: string;
  args?: string[];
  allowedCallers: Array<'extension-page' | 'content-script'>;
  maxRequestBytes?: number;
  maxResponseBytes?: number;
  timeoutMs?: number;
}

export interface BridgeManifest {
  extensionId: string;
  extensionPageOrigin?: string;
  contentScripts?: string[];
  hosts: Record<string, HostSpec>;
}

export interface BridgeRequest {
  id: string;
  app: string;
  payload: unknown;
}

export type BridgeResponse =
  | { ok: true; id: string; result: unknown }
  | { ok: false; id: string | null; error: { code: string; message: string } };

export class BridgeError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

const DEFAULT_MAX_REQUEST_BYTES = 64 * 1024;
const DEFAULT_MAX_RESPONSE_BYTES = 1024 * 1024;
const DEFAULT_TIMEOUT_MS = 10_000;
const MAX_ID_LENGTH = 128;
const MAX_APP_LENGTH = 256;
const SESSION_TTL_MS = 30_000;
const ID_PATTERN = /^[A-Za-z0-9_-]+$/;
const REQUEST_FIELDS = new Set(['id', 'app', 'payload']);

interface Session {
  tokenHash: Buffer;
  scriptId: string;
  expiresAt: number;
}

export class NativeBridge {
  private readonly manifest: BridgeManifest;
  private readonly extensionOrigin: string;
  private readonly declaredScripts: Set<string>;
  private readonly sessions = new Map<string, Session>();

  constructor(manifest: BridgeManifest) {
    if (!manifest || typeof manifest.extensionId !== 'string' || manifest.extensionId.length === 0) {
      throw new BridgeError('ERR_MANIFEST', 'manifest requires an extensionId');
    }
    if (!manifest.hosts || typeof manifest.hosts !== 'object') {
      throw new BridgeError('ERR_MANIFEST', 'manifest requires a hosts allowlist');
    }
    for (const [name, spec] of Object.entries(manifest.hosts)) {
      if (!spec || typeof spec.command !== 'string' || spec.command.length === 0) {
        throw new BridgeError('ERR_MANIFEST', `host entry invalid`);
      }
      if (!Array.isArray(spec.allowedCallers) || spec.allowedCallers.length === 0) {
        throw new BridgeError('ERR_MANIFEST', `host entry invalid`);
      }
      void name;
    }
    this.manifest = manifest;
    this.extensionOrigin =
      manifest.extensionPageOrigin ?? `chrome-extension://${manifest.extensionId}`;
    this.declaredScripts = new Set(manifest.contentScripts ?? []);
  }

  issueContentScriptSession(scriptId: string, ttlMs: number = SESSION_TTL_MS): { token: string; expiresAt: number } {
    if (!this.declaredScripts.has(scriptId)) {
      throw new BridgeError('ERR_FORBIDDEN', 'content script is not declared');
    }
    const token = randomBytes(32).toString('hex');
    const expiresAt = Date.now() + ttlMs;
    this.sessions.set(token, {
      tokenHash: hashToken(token),
      scriptId,
      expiresAt,
    });
    return { token, expiresAt };
  }

  async handleMessage(rawMessage: unknown, caller: CallerContext): Promise<BridgeResponse> {
    let requestId: string | null = null;
    try {
      this.authorizeCaller(caller);
      const request = this.validateRequest(rawMessage);
      requestId = request.id;
      const spec = this.lookupHost(request.app, caller.kind as 'extension-page' | 'content-script');
      const result = await this.exchange(spec, request);
      return { ok: true, id: request.id, result };
    } catch (err) {
      if (err instanceof BridgeError) {
        return { ok: false, id: requestId, error: { code: err.code, message: err.message } };
      }
      return {
        ok: false,
        id: requestId,
        error: { code: 'ERR_INTERNAL', message: 'native bridge failure' },
      };
    }
  }

  private authorizeCaller(caller: CallerContext): void {
    if (!caller || typeof caller !== 'object') {
      throw new BridgeError('ERR_FORBIDDEN', 'caller is not authorized');
    }
    switch (caller.kind) {
      case 'web-page':
        throw new BridgeError('ERR_FORBIDDEN', 'web pages may not use the native bridge');
      case 'extension-page':
        if (caller.extensionId !== this.manifest.extensionId || caller.origin !== this.extensionOrigin) {
          throw new BridgeError('ERR_FORBIDDEN', 'caller is not authorized');
        }
        return;
      case 'content-script':
        if (caller.extensionId !== this.manifest.extensionId) {
          throw new BridgeError('ERR_FORBIDDEN', 'caller is not authorized');
        }
        if (!caller.scriptId || !this.declaredScripts.has(caller.scriptId)) {
          throw new BridgeError('ERR_FORBIDDEN', 'content script is not declared');
        }
        this.consumeSession(caller.scriptId, caller.sessionToken);
        return;
      default:
        throw new BridgeError('ERR_FORBIDDEN', 'caller is not authorized');
    }
  }

  private consumeSession(scriptId: string, token: string | undefined): void {
    if (!token || typeof token !== 'string') {
      throw new BridgeError('ERR_SESSION', 'missing session token');
    }
    const session = this.sessions.get(token);
    if (!session) {
      throw new BridgeError('ERR_SESSION', 'invalid or consumed session token');
    }
    this.sessions.delete(token);
    if (session.expiresAt < Date.now()) {
      throw new BridgeError('ERR_SESSION', 'session token expired');
    }
    if (session.scriptId !== scriptId || !timingSafeEqual(session.tokenHash, hashToken(token))) {
      throw new BridgeError('ERR_SESSION', 'session token mismatch');
    }
  }

  private validateRequest(raw: unknown): BridgeRequest {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
      throw new BridgeError('ERR_BAD_REQUEST', 'request must be an object');
    }
    const record = raw as Record<string, unknown>;
    for (const key of Object.keys(record)) {
      if (!REQUEST_FIELDS.has(key)) {
        throw new BridgeError('ERR_BAD_REQUEST', 'request contains unexpected fields');
      }
    }
    const { id, app, payload } = record;
    if (typeof id !== 'string' || id.length === 0 || id.length > MAX_ID_LENGTH || !ID_PATTERN.test(id)) {
      throw new BridgeError('ERR_BAD_REQUEST', 'request id is invalid');
    }
    if (typeof app !== 'string' || app.length === 0 || app.length > MAX_APP_LENGTH) {
      throw new BridgeError('ERR_BAD_REQUEST', 'application name is invalid');
    }
    if (!('payload' in record)) {
      throw new BridgeError('ERR_BAD_REQUEST', 'request payload is missing');
    }
    return { id, app, payload };
  }

  private lookupHost(app: string, kind: 'extension-page' | 'content-script'): HostSpec {
    const spec = this.manifest.hosts[app];
    if (!spec) {
      throw new BridgeError('ERR_UNKNOWN_APP', 'application is not permitted');
    }
    if (!spec.allowedCallers.includes(kind)) {
      throw new BridgeError('ERR_FORBIDDEN', 'caller may not invoke this application');
    }
    return spec;
  }

  private async exchange(spec: HostSpec, request: BridgeRequest): Promise<unknown> {
    const maxRequestBytes = spec.maxRequestBytes ?? DEFAULT_MAX_REQUEST_BYTES;
    const maxResponseBytes = spec.maxResponseBytes ?? DEFAULT_MAX_RESPONSE_BYTES;
    const timeoutMs = spec.timeoutMs ?? DEFAULT_TIMEOUT_MS;

    const body = Buffer.from(JSON.stringify({ id: request.id, payload: request.payload }), 'utf8');
    if (body.length > maxRequestBytes) {
      throw new BridgeError('ERR_REQUEST_TOO_LARGE', 'request payload is too large');
    }
    const header = Buffer.alloc(4);
    header.writeUInt32LE(body.length, 0);
    const framed = Buffer.concat([header, body]);

    return new Promise((resolve, reject) => {
      let settled = false;
      let child;
      try {
        child = spawn(spec.command, spec.args ?? [], {
          env: {},
          shell: false,
          stdio: ['pipe', 'pipe', 'ignore'],
          windowsHide: true,
        });
      } catch {
        reject(new BridgeError('ERR_HOST_UNAVAILABLE', 'native host unavailable'));
        return;
      }

      const finish = (err: BridgeError | null, value?: unknown) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        child.removeAllListeners();
        child.stdout.removeAllListeners();
        if (!child.killed) child.kill('SIGKILL');
        if (err) reject(err);
        else resolve(value);
      };

      const timer = setTimeout(() => {
        finish(new BridgeError('ERR_TIMEOUT', 'native host timed out'));
      }, timeoutMs);

      let buffer = Buffer.alloc(0);
      child.stdout.on('data', (chunk: Buffer) => {
        if (settled) return;
        buffer = Buffer.concat([buffer, chunk]);
        if (buffer.length > maxResponseBytes + 4) {
          finish(new BridgeError('ERR_RESPONSE_TOO_LARGE', 'native host response too large'));
          return;
        }
        while (buffer.length >= 4) {
          const length = buffer.readUInt32LE(0);
          if (length > maxResponseBytes) {
            finish(new BridgeError('ERR_RESPONSE_TOO_LARGE', 'native host response too large'));
            return;
          }
          if (buffer.length < 4 + length) return;
          const raw = buffer.subarray(4, 4 + length);
          buffer = buffer.subarray(4 + length);
          let parsed: unknown;
          try {
            parsed = JSON.parse(raw.toString('utf8'));
          } catch {
            finish(new BridgeError('ERR_BAD_RESPONSE', 'native host sent an invalid response'));
            return;
          }
          finish(null, parsed);
          return;
        }
      });

      child.on('error', () => {
        finish(new BridgeError('ERR_HOST_UNAVAILABLE', 'native host unavailable'));
      });
      child.on('exit', () => {
        finish(new BridgeError('ERR_HOST_EXITED', 'native host exited unexpectedly'));
      });

      child.stdin.on('error', () => {
        finish(new BridgeError('ERR_HOST_UNAVAILABLE', 'native host unavailable'));
      });
      child.stdin.write(framed);
      child.stdin.end();
    });
  }
}

function hashToken(token: string): Buffer {
  return Buffer.from(token, 'utf8');
}
