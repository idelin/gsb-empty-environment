export {
  createErrorEgressInterceptor,
  sanitizeError,
  sanitizeDetails,
  sanitizeMetadata,
  redactMessage,
  isForbiddenMetadataKey,
} from "./src/error-egress.ts";
export type {
  AnyDetail,
  Logger,
  ErrorEgressOptions,
} from "./src/error-egress.ts";
