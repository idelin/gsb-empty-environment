import { parseStringDictionary } from "./structured-fields.ts";
import { classifyHost } from "./ip.ts";

/**
 * Parses a Reporting-Endpoints header value into a map of endpoint name to
 * absolute URL, keeping only endpoints that are safe to deliver reports to.
 *
 * - The header is parsed as an RFC 8941 dictionary of strings; any parse
 *   failure invalidates the whole header.
 * - Values are resolved against the response URL; the result must be HTTPS
 *   and must not carry userinfo.
 * - Endpoints on loopback, private, or link-local addresses (including
 *   integer IPv4 and IPv4-mapped IPv6 spellings) are dropped unless the
 *   document itself was served from the same address space.
 */
export function parseReportingEndpoints(
  headerValue: string,
  responseUrl: string,
): Map<string, string> {
  const endpoints = new Map<string, string>();
  const declared = parseStringDictionary(headerValue);
  if (declared === null) return endpoints;

  let base: URL;
  try {
    base = new URL(responseUrl);
  } catch {
    return endpoints;
  }
  const documentSpace = classifyHost(base.hostname);

  for (const [name, rawUrl] of declared) {
    let url: URL;
    try {
      url = new URL(rawUrl, base);
    } catch {
      continue;
    }
    if (url.protocol !== "https:") continue;
    if (url.username !== "" || url.password !== "") continue;
    const space = classifyHost(url.hostname);
    if (space !== "public" && space !== documentSpace) continue;
    endpoints.set(name, url.href);
  }
  return endpoints;
}
