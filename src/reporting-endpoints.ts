import { parseDictionaryHeaders } from "./structured-dict.ts";

/**
 * Parse Reporting-Endpoints header field values into a map of endpoint
 * group name -> absolute URL.
 *
 * Rules (Reporting API + RFC 8941):
 * - Multiple field values are combined and parsed as one dictionary; a
 *   syntax error anywhere invalidates the whole header.
 * - Member values must be strings; non-string members are skipped.
 * - Values are resolved against the response URL (relative allowed).
 * - Only https: URLs without username/password are accepted.
 */
export function parseReportingEndpoints(
  fieldValues: string[],
  responseUrl: string | URL,
): Map<string, URL> {
  const endpoints = new Map<string, URL>();
  const dict = parseDictionaryHeaders(fieldValues);
  if (dict === null) return endpoints; // parse failure: ignore entire header
  let base: URL;
  try {
    base = new URL(responseUrl);
  } catch {
    return endpoints;
  }
  for (const [name, member] of dict) {
    if (member.value.kind !== "string") continue;
    let url: URL;
    try {
      url = new URL(member.value.value, base);
    } catch {
      continue;
    }
    if (url.protocol !== "https:") continue;
    if (url.username !== "" || url.password !== "") continue;
    endpoints.set(name, url);
  }
  return endpoints;
}
