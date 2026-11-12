export { SharedWorkerProxy, createBrowserWorkerFactory } from './client-proxy.ts';
export type { ConnectRequest, ConnectedTenantChannel } from './client-proxy.ts';
export { TenantWorkerAgent } from './worker-agent.ts';
export type { WorkerAgentOptions } from './worker-agent.ts';
export { DemoTenantRouter } from './demo-router.ts';
export { deriveWorkerName, canonicalScriptUrl } from './worker-name.ts';
export {
  issueHandshakeToken,
  verifyHandshakeToken,
  randomNonce,
} from './token.ts';
export type { TokenClaims, TokenIssue, TokenVerifyResult } from './token.ts';
export { InMemoryReplayCache } from './replay-cache.ts';
export type { ReplayCache } from './replay-cache.ts';
export { WorkerProxyError, handshakeError } from './errors.ts';
export {
  HANDSHAKE_PROTOCOL,
  isHandshakeRequest,
} from './protocol.ts';
export type {
  PortLike,
  MessagePortLike,
  MessageChannelLike,
  MessageChannelFactory,
  SharedWorkerLike,
  WorkerFactory,
  TenantCredentials,
  TenantRouter,
  TenantChannel,
  TenantSecretResolver,
  HandshakeErrorCode,
  HandshakeRequestPayload,
  HandshakeResult,
} from './protocol.ts';
