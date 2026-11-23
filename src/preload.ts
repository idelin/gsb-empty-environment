import { BridgeError } from './errors.js';
import type { ChannelRegistry, RegisteredChannel } from './registry.js';
import { scanValue } from './sanitize.js';

/** Minimal subset of Electron's IpcRenderer used by the bridge. */
export interface IpcRendererLike {
  invoke(channel: string, ...args: unknown[]): Promise<unknown>;
}

export type BridgeApi = Record<string, (arg?: unknown) => Promise<unknown>>;

function invokeFor(renderer: IpcRendererLike, entry: RegisteredChannel) {
  return async function invoke(arg?: unknown): Promise<unknown> {
    // Defense in depth: reject functions/getters/Proxy traps before they reach
    // the IPC serializer. The main process repeats the full pipeline anyway.
    scanValue(arg);
    try {
      return await renderer.invoke(entry.channel, arg);
    } catch (error) {
      if (error instanceof BridgeError) {
        throw error;
      }
      const message = error instanceof Error ? error.message : String(error);
      throw new BridgeError(`IPC invocation failed for ${entry.exposedName}: ${message}`);
    }
  };
}

/**
 * Builds the object handed to contextBridge.exposeInMainWorld. Only names
 * present in the registry are defined; the object is frozen and carries no
 * prototype-chain surface of its own.
 */
export function createBridgeApi(registry: ChannelRegistry, renderer: IpcRendererLike): BridgeApi {
  const surface: Record<string, (arg?: unknown) => Promise<unknown>> = Object.create(null);

  for (const entry of registry.list()) {
    Object.defineProperty(surface, entry.exposedName, {
      value: invokeFor(renderer, entry),
      writable: false,
      enumerable: true,
      configurable: false,
    });
  }

  return Object.freeze(surface);
}
