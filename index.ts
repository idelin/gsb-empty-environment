export {
  HostnameError,
  normalizeHostname,
  parseNamePattern,
  hostnameMatchesPattern,
  mxMatchesPolicyEntry,
  certificateMatchesMx,
} from "./src/hostname.ts";

export {
  MtaStsPolicyError,
  parsePolicy,
  parseStsTxtRecords,
} from "./src/policy.ts";
export type { MtaStsMode, MtaStsPolicy, MtaStsTxtRecord } from "./src/policy.ts";

export { InMemoryPolicyCache } from "./src/cache.ts";
export type { CachedPolicy, CacheSnapshot, PolicyCache } from "./src/cache.ts";

export {
  MtaStsClient,
  DefaultHttpsFetcher,
  consoleLogger,
} from "./src/client.ts";
export type {
  DnsResolver,
  HttpsFetcher,
  HttpsResponse,
  Logger,
  DeliveryAction,
  MtaStsReason,
  EvaluationInput,
  EvaluationResult,
  MtaStsClientOptions,
} from "./src/client.ts";

export { NodeDnsResolver } from "./src/node-dns.ts";
