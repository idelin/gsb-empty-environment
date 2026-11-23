import { contextBridge, ipcRenderer } from 'electron';
import { createBridgeApi, type BridgeApi } from './preload.js';
import type { ChannelRegistry } from './registry.js';

/**
 * Preload entry point. Import this module from the preload script and call
 * exposeBridge once, passing the same registry used in the main process.
 */
export function exposeBridge(apiKey: string, registry: ChannelRegistry): BridgeApi {
  if (typeof apiKey !== 'string' || apiKey.length === 0 || apiKey === 'node') {
    throw new TypeError('apiKey must be a non-empty string other than "node"');
  }
  const api = createBridgeApi(registry, ipcRenderer);
  contextBridge.exposeInMainWorld(apiKey, api);
  return api;
}
