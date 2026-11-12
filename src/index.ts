export type {
  AccountResolution,
  AcctIdentifier,
  FetchHeaders,
  FetchInit,
  FetchLike,
  FetchResponse,
  HostMetaLink,
  Jrd,
  JrdLink,
  WebFingerOptions,
} from "./types.ts";

export {
  InvalidAcctError,
  InvalidUrlError,
  encodeAcctForQuery,
  hostnameOf,
  hostOfUrl,
  normalizeHost,
  parseAcct,
  sameHost,
  tryNormalizeHost,
  tryParseAcct,
  tryToHttpsUrl,
  toHttpsUrl,
} from "./uri.ts";

export {
  expandLrddTemplate,
  parseHostMetaLinks,
  selectLrddTemplate,
  validateLrddTemplate,
} from "./hostmeta.ts";

export { validateJrd } from "./jrd.ts";

export {
  fetchHttpsWithSameHostRedirects,
} from "./http.ts";
export type {
  SecureFetchFailure,
  SecureFetchOptions,
  SecureFetchResult,
} from "./http.ts";

export {
  discoverLrddTemplate,
  resolveAccount,
  resolveWebFinger,
} from "./resolver.ts";
