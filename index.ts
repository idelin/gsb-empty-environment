export {
  BridgeDispatcher,
  attachBridge,
  type BridgeConfig,
  type BridgeError,
  type BridgeFailure,
  type BridgeMessageEvent,
  type BridgeResponse,
  type BridgeSuccess,
  type DispatchResult,
  type PluginDefinition,
  type PluginHandler,
  type WindowLike,
} from './src/bridge.ts';
export {
  containsProtoKey,
  validateOptions,
} from './src/options-guard.ts';
export {
  hostMatchesPattern,
  isAllowedOrigin,
  originToHost,
  parsePattern,
  type OriginPattern,
  type ParsedPattern,
} from './src/origin-policy.ts';
