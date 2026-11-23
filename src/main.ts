import { SenderRejectedError } from './errors.js';
import type { ChannelRegistry } from './registry.js';
import { safeClone, scanValue } from './sanitize.js';
import { validate } from './schema.js';

/** Minimal structural subset of Electron's WebFrame used for sender checks. */
export interface SenderFrameLike {
  url: string;
  origin: string;
  top?: SenderFrameLike | null;
}

export interface IpcMainEventLike {
  senderFrame: SenderFrameLike;
}

export interface IpcMainLike {
  handle(channel: string, listener: (event: IpcMainEventLike, ...args: unknown[]) => unknown): void;
  removeHandler?(channel: string): void;
}

export interface FramePolicy {
  /** Exact origins allowed to invoke the bridge, e.g. "https://app.example.com". */
  allowedOrigins: readonly string[];
  /** Reject outermost iframes; defaults to true. */
  requireMainFrame?: boolean;
  /** Optional custom check in addition to the origin allow-list. */
  verify?: (frame: SenderFrameLike) => boolean;
}

export type BridgeHandler = (
  event: IpcMainEventLike,
  channel: string,
  safeArg: unknown,
) => unknown | Promise<unknown>;

function isAllowedOrigin(frame: SenderFrameLike, allowedOrigins: readonly string[]): boolean {
  let frameOrigin: string;
  try {
    frameOrigin = frame.origin || new URL(frame.url).origin;
  } catch {
    return false;
  }
  return frameOrigin !== 'null' && allowedOrigins.includes(frameOrigin);
}

/**
 * Authorizes the sending frame. Rejects missing frames, subframes (unless
 * explicitly allowed), opaque origins and anything outside the allow-list.
 */
export function verifySenderFrame(event: IpcMainEventLike, policy: FramePolicy): SenderFrameLike {
  const frame = event?.senderFrame;
  if (!frame || typeof frame.url !== 'string') {
    throw new SenderRejectedError('IPC sender frame is missing or unreadable');
  }

  if (policy.requireMainFrame !== false && frame.top !== frame) {
    throw new SenderRejectedError(`IPC from embedded frame rejected: ${frame.url}`);
  }
  if (!isAllowedOrigin(frame, policy.allowedOrigins)) {
    throw new SenderRejectedError(`IPC sender origin is not allowed: ${frame.url}`);
  }
  if (policy.verify !== undefined && !policy.verify(frame)) {
    throw new SenderRejectedError(`IPC sender failed custom verification: ${frame.url}`);
  }

  return frame;
}

/**
 * Full main-process argument pipeline: clone out of the IPC object, reject
 * functions/getters/pollution keys, then validate against the registered
 * schema. Returns the verified snapshot the handler may safely consume.
 */
export function prepareHandlerArgument(
  rawArg: unknown,
  schema: Parameters<typeof validate>[1],
): unknown {
  const cloned = safeClone(rawArg);
  scanValue(cloned);
  validate(cloned, schema);
  return cloned;
}

/**
 * Registers one ipcMain.handle listener per registered channel. Unknown
 * channels never receive a privileged handler; sender frame, cloning and
 * schema validation happen before any user code runs.
 */
export function registerBridgeHandlers(
  ipcMain: IpcMainLike,
  registry: ChannelRegistry,
  policy: FramePolicy,
  handler: BridgeHandler,
): void {
  for (const entry of registry.list()) {
    ipcMain.handle(entry.channel, async (event: IpcMainEventLike, ...args: unknown[]) => {
      verifySenderFrame(event, policy);
      const safeArg = prepareHandlerArgument(args[0], entry.schema);
      return handler(event, entry.channel, safeArg);
    });
  }
}
