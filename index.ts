export {
  CONFIG_MANIFEST_KEY,
  CONFIG_MESSAGE_TYPE,
  CONFIG_PROTOCOL_VERSION,
  PAGE_OPERATIONS,
  isOperationAllowed,
  loadManifestConfig,
  narrowConfig,
  parseTrustedConfigMessage,
  tryLoadManifestConfig,
  type PageOperation,
  type SecurityConfig,
} from "./src/config.ts";

export {
  BRIDGE_HELLO_TYPE,
  BRIDGE_PROTOCOL_VERSION,
  BRIDGE_READY_TYPE,
  BRIDGE_REQUEST_TYPE,
  BRIDGE_RESPONSE_TYPE,
  OneTimePageBridge,
  type ContentWindowLike,
  type OneTimePageBridgeOptions,
  type PrivilegedDispatchResult,
  type PrivilegedDispatcher,
  type ValidatedPageRequest,
} from "./src/bridge.ts";

export {
  findPageAnchor,
  selectUniqueStructuralPath,
  type StructuralContainer,
  type StructuralNode,
  type StructuralSelection,
} from "./src/dom.ts";

export {
  ExtensionContentScript,
  isTrustedExtensionSender,
  type ContentScriptOptions,
  type RuntimeLike,
  type RuntimeMessageApi,
  type RuntimeMessageSender,
  type TrustedMessageResult,
} from "./src/content_script.ts";

export {
  HARD_LIMITS,
  ValidationError,
  type ValidationCode,
} from "./src/security.ts";
