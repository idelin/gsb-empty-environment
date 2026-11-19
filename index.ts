export {
  IrcProtocolError,
  ProtocolError,
  SerializationError,
} from './src/errors.ts';

export {
  escapeTagValue,
  unescapeTagValue,
  parseTagString,
  serializeTags,
  selectClientTags,
  type TagMap,
} from './src/tags.ts';

export { parseLine, LineDecoder, type IrcMessage, type DecoderResult } from './src/parser.ts';
export { serializeMessage, type OutgoingMessage } from './src/serialize.ts';

export {
  CAP_MESSAGE_TAGS,
  CapabilityRegistry,
  ConnectionCapState,
  CapNegotiator,
  type CapNegotiationReply,
} from './src/cap.ts';

export { RegistrationGate, type RejectedCommand } from './src/session.ts';

export {
  forwardMessage,
  buildForwardLine,
  type ForwardTarget,
  type ForwardResult,
} from './src/forward.ts';
