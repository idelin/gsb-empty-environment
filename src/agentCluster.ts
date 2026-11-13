/**
 * Parsing of the `Origin-Agent-Cluster` response header.
 *
 * The header carries a Structured Fields boolean. Only the value `?1`
 * requests an origin-keyed agent cluster; `?0`, an absent header, or any
 * malformed value do not. An origin-keyed agent cluster makes the
 * `document.domain` setter throw and keeps strict (scheme + host + port)
 * origin comparisons.
 *
 * See the HTML spec, "Is origin-keyed?" and the structured fields boolean.
 */

/**
 * @param headerValue the raw `Origin-Agent-Cluster` header value, or
 *   `null`/`undefined` when the header is absent.
 * @returns `true` only when the header is exactly the boolean `?1`.
 */
export function requestsOriginKeyedAgentCluster(
  headerValue: string | null | undefined,
): boolean {
  if (headerValue === null || headerValue === undefined) {
    return false;
  }
  return headerValue.trim() === "?1";
}

/**
 * Convenience helper for a header bag such as a `Response`/`Headers` object
 * or a plain lower-cased record.
 */
export function isOriginKeyed(
  headers: Headers | Record<string, string>,
): boolean {
  const value =
    typeof (headers as Headers).get === "function"
      ? (headers as Headers).get("origin-agent-cluster")
      : (headers as Record<string, string>)["origin-agent-cluster"] ??
        (headers as Record<string, string>)["Origin-Agent-Cluster"];
  return requestsOriginKeyedAgentCluster(value);
}
