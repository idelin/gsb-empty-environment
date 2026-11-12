import {
  isAllowedOrigin,
  parsePattern,
  type OriginPattern,
  type ParsedPattern,
} from './origin-policy.ts';
import { hasOwn, validateOptions } from './options-guard.ts';

export type PluginHandler = (
  options: Record<string, unknown> | undefined,
) => unknown | Promise<unknown>;

export interface PluginDefinition {
  readonly methods: Record<string, PluginHandler>;
}

export interface BridgeConfig {
  /** Allowed source hosts/origins. */
  readonly allowNavigation: readonly OriginPattern[];
  /** Explicit plugin allow-list. */
  readonly plugins: Record<string, PluginDefinition>;
}

export type BridgeErrorCode =
  | 'ORIGIN_DENIED'
  | 'IFRAME_FORBIDDEN'
  | 'INVALID_MESSAGE'
  | 'PLUGIN_NOT_ALLOWED'
  | 'METHOD_NOT_ALLOWED'
  | 'OPTIONS_REJECTED'
  | 'DUPLICATE_CALLBACK'
  | 'PLUGIN_ERROR';

export interface BridgeError {
  readonly code: BridgeErrorCode;
  readonly message: string;
}

export interface BridgeSuccess {
  readonly ok: true;
  readonly callbackId: string;
  readonly pluginId: string;
  readonly methodName: string;
  readonly result: unknown;
}

export interface BridgeFailure {
  readonly ok: false;
  readonly callbackId: string | null;
  readonly pluginId: string | null;
  readonly methodName: string | null;
  readonly error: BridgeError;
  /** True when no response must be posted back (untrusted or re-used id). */
  readonly respond: boolean;
}

export type DispatchResult = BridgeSuccess | BridgeFailure;

/** Minimal MessageEvent shape the dispatcher needs. */
export interface BridgeMessageEvent {
  readonly origin: string;
  readonly source: unknown;
  readonly data: unknown;
}

export interface BridgeResponse {
  readonly callbackId: string;
  readonly pluginId: string;
  readonly methodName: string;
  readonly success: boolean;
  readonly result?: unknown;
  readonly error?: BridgeError;
}

export interface WindowLike {
  addEventListener(
    type: 'message',
    listener: (event: BridgeMessageEvent) => void,
  ): void;
  removeEventListener(
    type: 'message',
    listener: (event: BridgeMessageEvent) => void,
  ): void;
  postMessage(message: BridgeResponse, targetOrigin: string): void;
  readonly self?: unknown;
  readonly top?: unknown;
}

const CALLBACK_ID_PATTERN = /^[A-Za-z0-9]{1,32}$/;
const IDENTIFIER_PATTERN = /^[A-Za-z][A-Za-z0-9_]{0,63}$/;

/** Names that must never resolve through the prototype chain. */
const PROTOTYPE_KEYS = new Set(['__proto__', 'constructor', 'prototype']);

function failure(
  error: BridgeError,
  fields: {
    callbackId?: string | null;
    pluginId?: string | null;
    methodName?: string | null;
    respond: boolean;
  },
): BridgeFailure {
  return {
    ok: false,
    callbackId: fields.callbackId ?? null,
    pluginId: fields.pluginId ?? null,
    methodName: fields.methodName ?? null,
    error,
    respond: fields.respond,
  };
}

/**
 * Build a null-prototype method map from a definition. Enumerable own
 * entries are copied so later mutation of the input cannot widen the
 * allow-list, and lookups never traverse Object.prototype.
 */
function createMethodMap(definition: PluginDefinition): Record<string, PluginHandler> {
  const map: Record<string, PluginHandler> = Object.create(null) as Record<string, PluginHandler>;
  for (const name of Object.keys(definition.methods)) {
    if (PROTOTYPE_KEYS.has(name) || !IDENTIFIER_PATTERN.test(name)) {
      throw new Error(`invalid bridge method name: ${name}`);
    }
    const handler = definition.methods[name];
    if (typeof handler !== 'function') {
      throw new Error(`bridge method is not a function: ${name}`);
    }
    map[name] = handler;
  }
  return map;
}

export class BridgeDispatcher {
  private readonly allowedPatterns: readonly ParsedPattern[];
  private readonly plugins: Record<string, Record<string, PluginHandler>>;
  private readonly settledCallbacks = new Set<string>();

  constructor(config: BridgeConfig) {
    this.allowedPatterns = config.allowNavigation.map(parsePattern);
    const registry: Record<string, Record<string, PluginHandler>> = Object.create(null) as Record<
      string,
      Record<string, PluginHandler>
    >;
    for (const pluginId of Object.keys(config.plugins)) {
      if (PROTOTYPE_KEYS.has(pluginId) || !IDENTIFIER_PATTERN.test(pluginId)) {
        throw new Error(`invalid bridge plugin id: ${pluginId}`);
      }
      registry[pluginId] = createMethodMap(config.plugins[pluginId]);
    }
    this.plugins = registry;
  }

  private resolveHandler(
    pluginId: string,
    methodName: string,
  ): { ok: true; handler: PluginHandler } | { ok: false; code: BridgeErrorCode; message: string } {
    if (!hasOwn(this.plugins, pluginId)) {
      return {
        ok: false,
        code: 'PLUGIN_NOT_ALLOWED',
        message: `plugin not allowed: ${pluginId}`,
      };
    }
    const methods = this.plugins[pluginId];
    if (!hasOwn(methods, methodName)) {
      return {
        ok: false,
        code: 'METHOD_NOT_ALLOWED',
        message: `method not allowed: ${pluginId}.${methodName}`,
      };
    }
    return { ok: true, handler: methods[methodName] };
  }

  /**
   * Dispatch one validated bridge message. Never throws: plugin errors are
   * reported as failures so the caller can answer the web side.
   *
   * @param event raw message event
   * @param sourceIsHostWindow true only when event.source is the window
   *   hosting this dispatcher; iframe/pop-up/opener sources pass false.
   */
  async dispatch(event: BridgeMessageEvent, sourceIsHostWindow: boolean): Promise<DispatchResult> {
    const data = event.data as {
      callbackId?: unknown;
      pluginId?: unknown;
      methodName?: unknown;
      options?: unknown;
    } | null;

    if (data === null || typeof data !== 'object' || Array.isArray(data)) {
      return failure(
        { code: 'INVALID_MESSAGE', message: 'message payload must be an object' },
        { respond: false },
      );
    }

    const { callbackId, pluginId, methodName, options } = data;

    if (typeof callbackId !== 'string' || !CALLBACK_ID_PATTERN.test(callbackId)) {
      return failure(
        { code: 'INVALID_MESSAGE', message: 'callbackId must be 1-32 alphanumeric characters' },
        { respond: false },
      );
    }

    // Single completion: a re-used id is consumed silently so a hostile
    // re-send can neither invoke a plugin nor generate a second response.
    if (this.settledCallbacks.has(callbackId)) {
      return failure(
        { code: 'DUPLICATE_CALLBACK', message: `callbackId already used: ${callbackId}` },
        { callbackId, respond: false },
      );
    }

    if (
      typeof pluginId !== 'string' ||
      typeof methodName !== 'string' ||
      PROTOTYPE_KEYS.has(pluginId) ||
      PROTOTYPE_KEYS.has(methodName) ||
      !IDENTIFIER_PATTERN.test(pluginId) ||
      !IDENTIFIER_PATTERN.test(methodName)
    ) {
      return failure(
        { code: 'INVALID_MESSAGE', message: 'pluginId/methodName must be non-prototype identifiers' },
        {
          callbackId,
          pluginId: typeof pluginId === 'string' ? pluginId : null,
          methodName: typeof methodName === 'string' ? methodName : null,
          respond: true,
        },
      );
    }

    // Origin and frame checks happen after shape validation but before any
    // allow-list lookup or plugin execution.
    if (!isAllowedOrigin(event.origin, this.allowedPatterns)) {
      return failure(
        { code: 'ORIGIN_DENIED', message: `origin not allowed: ${event.origin}` },
        { callbackId, pluginId, methodName, respond: true },
      );
    }

    if (!sourceIsHostWindow) {
      return failure(
        { code: 'IFRAME_FORBIDDEN', message: 'iframes and cross-window callers have no bridge access' },
        { callbackId, pluginId, methodName, respond: true },
      );
    }

    const optionsCheck = validateOptions(options);
    if (!optionsCheck.ok) {
      return failure(
        { code: 'OPTIONS_REJECTED', message: optionsCheck.reason },
        { callbackId, pluginId, methodName, respond: true },
      );
    }

    const resolved = this.resolveHandler(pluginId, methodName);
    if (!resolved.ok) {
      return failure(
        { code: resolved.code, message: resolved.message },
        { callbackId, pluginId, methodName, respond: true },
      );
    }

    // Reserve the id before invoking the handler so concurrent calls with
    // the same id cannot both execute.
    this.settledCallbacks.add(callbackId);

    try {
      const result = await resolved.handler(options as Record<string, unknown> | undefined);
      return { ok: true, callbackId, pluginId, methodName, result };
    } catch (cause) {
      return failure(
        {
          code: 'PLUGIN_ERROR',
          message: cause instanceof Error ? cause.message : String(cause),
        },
        { callbackId, pluginId, methodName, respond: true },
      );
    }
  }

  /** True once a callbackId has completed (success or handler failure). */
  isCallbackSettled(callbackId: string): boolean {
    return this.settledCallbacks.has(callbackId);
  }
}

/**
 * Bind a dispatcher to a window's message events. Returns a detach
 * function. Responses are only posted for failures flagged `respond` and
 * for successful dispatches; untrusted/duplicate traffic is dropped.
 */
export function attachBridge(
  dispatcher: BridgeDispatcher,
  targetWindow: WindowLike,
): () => void {
  const listener = (event: BridgeMessageEvent): void => {
    // Only the hosting window itself may invoke the bridge. An embedded
    // iframe, pop-up or opener produces a different source reference.
    const hostReference = targetWindow.self ?? targetWindow;
    const sourceIsHostWindow = event.source === hostReference;

    void dispatcher.dispatch(event, sourceIsHostWindow).then((result) => {
      if (!result.ok) {
        if (!result.respond || result.callbackId === null) {
          return;
        }
        targetWindow.postMessage(
          {
            callbackId: result.callbackId,
            pluginId: result.pluginId ?? '',
            methodName: result.methodName ?? '',
            success: false,
            error: result.error,
          },
          event.origin,
        );
        return;
      }
      targetWindow.postMessage(
        {
          callbackId: result.callbackId,
          pluginId: result.pluginId,
          methodName: result.methodName,
          success: true,
          result: result.result,
        },
        event.origin,
      );
    });
  };

  targetWindow.addEventListener('message', listener);
  return () => targetWindow.removeEventListener('message', listener);
}
