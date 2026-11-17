export {
  EventLoop,
  Platform,
  PlatformMessageEvent,
  SimulatedDocument,
  SimulatedWindow,
} from './src/platform.ts';
export type {
  DocumentGeneration,
  MessageListener,
  TrustedMessageMeta,
} from './src/platform.ts';
export { ReplyCapability, SecureMessageReceiver } from './src/secure-receiver.ts';
export type {
  SecureMessage,
  SecureMessageHandler,
  SecureReceiverOptions,
} from './src/secure-receiver.ts';
