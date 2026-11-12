import { BridgeError } from './errors.ts';
import { BLOCKED_KEYS, s, type Schema } from './schema.ts';
import { sanitizeArguments } from './sanitize.ts';

export const BRIDGE_CHANNEL = 'secure-bridge:invoke';

export interface IpcMainLike {
  handle(
    channel: string,
    listener: (event: unknown, payload: unknown) => unknown,
  ): void;
  removeHandler(channel: string): void;
}

export interface ContextBridgeLike {
  exposeInMainWorld(apiKey: string, api: unknown): void;
}

export interface FrameLike {
  readonly url: string;
}

export interface IpcRendererLike {
  invoke(channel: string, payload: unknown): Promise<unknown>;
}

export interface BridgeEvent {
  senderFrame?: FrameLike;
}

export type OriginPolicy =
  | string
  | ReadonlySet<string>
  | ((url: URL) => boolean);

export type BridgeHandler = (
  event: BridgeEvent,
  ...args: unknown[]
) => unknown | Promise<unknown>;

export interface BridgeMethodDef {
  readonly params?: readonly Schema[];
  readonly allow?: OriginPolicy;
  readonly handler: BridgeHandler;
}

export type BridgeDefinition = Record<string, BridgeMethodDef>;

type ExposedApi<Def extends BridgeDefinition> = {
  [K in keyof Def]: (...args: unknown[]) => Promise<unknown>;
};

const envelopeSchema = s.object({
  method: s.field(s.string()),
  args: s.field(s.array(s.unknown())),
});

function hasOwn(target: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(target, key);
}

function assertDefinition(definition: BridgeDefinition): void {
  for (const name of Object.keys(definition)) {
    if (BLOCKED_KEYS.has(name)) {
      throw new BridgeError(
        'FORBIDDEN_KEY',
        `method name "${name}" is reserved and cannot be registered`,
      );
    }
  }
}

export function originAllowed(policy: OriginPolicy | undefined, url: URL): boolean {
  if (policy === undefined) {
    return true;
  }
  if (typeof policy === 'string') {
    return url.origin === policy;
  }
  if (policy instanceof Set) {
    return policy.has(url.origin);
  }
  try {
    return policy(url);
  } catch {
    return false;
  }
}

function parseFrameUrl(event: BridgeEvent): URL | undefined {
  const rawUrl = event.senderFrame?.url;
  if (typeof rawUrl !== 'string' || rawUrl.length === 0) {
    return undefined;
  }
  try {
    return new URL(rawUrl);
  } catch {
    return undefined;
  }
}

export function registerBridge(
  ipcMain: IpcMainLike,
  definition: BridgeDefinition,
  defaultAllow?: OriginPolicy,
): () => void {
  assertDefinition(definition);

  ipcMain.handle(BRIDGE_CHANNEL, async (event: unknown, payload: unknown) => {
    const ipcEvent = event as BridgeEvent;

    try {
      const envelope = sanitizeArguments([payload], [envelopeSchema])[0] as {
        method: string;
        args: unknown[];
      };

      if (!hasOwn(definition, envelope.method)) {
        throw new BridgeError(
          'UNREGISTERED_METHOD',
          `method "${envelope.method}" is not registered`,
        );
      }

      const method = definition[envelope.method];
      if (!method) {
        throw new BridgeError(
          'UNREGISTERED_METHOD',
          `method "${envelope.method}" is not registered`,
        );
      }
      const frameUrl = parseFrameUrl(ipcEvent);
      if (frameUrl === undefined) {
        throw new BridgeError(
          'FORBIDDEN_ORIGIN',
          'sender frame URL is missing or invalid',
        );
      }

      const allowed =
        method.allow !== undefined
          ? originAllowed(method.allow, frameUrl)
          : originAllowed(defaultAllow, frameUrl);
      if (!allowed) {
        throw new BridgeError(
          'FORBIDDEN_ORIGIN',
          `origin "${frameUrl.origin}" is not allowed to call "${envelope.method}"`,
        );
      }

      const safeArgs = sanitizeArguments(
        envelope.args,
        method.params ?? [],
      );
      return await method.handler(ipcEvent, ...safeArgs);
    } catch (error) {
      if (error instanceof BridgeError) {
        throw error;
      }
      if (error instanceof Error && error.name === 'DataCloneError') {
        throw new BridgeError(
          'NON_CLONEABLE_ARGUMENT',
          `$: value cannot be structured-cloned (${error.message})`,
        );
      }
      throw error;
    }
  });

  return () => {
    ipcMain.removeHandler(BRIDGE_CHANNEL);
  };
}

export function exposeBridge<Def extends BridgeDefinition>(
  contextBridge: ContextBridgeLike,
  ipcRenderer: IpcRendererLike,
  definition: Def,
  apiKey = 'secureBridge',
): ExposedApi<Def> {
  assertDefinition(definition);

  const api = Object.create(null) as Record<string, unknown>;

  for (const name of Object.keys(definition)) {
    api[name] = (...args: unknown[]): Promise<unknown> =>
      ipcRenderer.invoke(BRIDGE_CHANNEL, {
        method: name,
        args: [...args],
      });
  }

  Object.freeze(api);
  contextBridge.exposeInMainWorld(apiKey, api);
  return api as ExposedApi<Def>;
}
