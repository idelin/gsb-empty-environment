export {
  classifyIp,
  isMorePublic,
  normalizeIp,
  parseIp,
  spaceRank,
  unwrapMappedV4,
  type IpSpace,
  type IpVersion,
  type ParsedIp,
} from './src/ip.ts';

export {
  buildPreflightCacheKey,
  DEFAULT_TTL_MS,
  MAX_TTL_MS,
  PrivateNetworkAccessGuard,
  type Authorization,
  type GuardOptions,
  type GuardRequest,
  type PreflightResponse,
  type PreflightTransport,
} from './src/preflight.ts';

export {
  InvalidIpError,
  PnaError,
  PrivateNetworkAccessDeniedError,
  RebindingDetectedError,
} from './src/errors.ts';
