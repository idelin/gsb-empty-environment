/**
 * A tuple origin: scheme + host + port.
 *
 * The port is part of the origin and is always preserved. A `null` port means
 * the scheme's default port (for example 80 for http, 443 for https).
 * An opaque origin is represented by `null` at the call sites that allow it.
 */
export interface Origin {
  readonly scheme: string;
  readonly host: string;
  readonly port: number | null;
}

const DEFAULT_PORTS: Readonly<Record<string, number>> = {
  http: 80,
  https: 443,
  ws: 80,
  wss: 443,
};

function normalizePort(scheme: string, port: number | null): number | null {
  if (port === null) {
    return null;
  }
  return DEFAULT_PORTS[scheme] === port ? null : port;
}

export function createOrigin(
  scheme: string,
  host: string,
  port: number | null = null,
): Origin {
  const normalizedScheme = scheme.toLowerCase();
  return {
    scheme: normalizedScheme,
    host: host.toLowerCase(),
    port: normalizePort(normalizedScheme, port),
  };
}

export function parseOrigin(url: string): Origin {
  const parsed = new URL(url);
  if (parsed.hostname === "" || parsed.protocol === "") {
    throw new TypeError(`URL "${url}" does not have an identifiable origin`);
  }
  return createOrigin(
    parsed.protocol.replace(/:$/, ""),
    parsed.hostname,
    parsed.port === "" ? null : Number(parsed.port),
  );
}

export function originToString(origin: Origin): string {
  const portPart = origin.port === null ? "" : `:${origin.port}`;
  return `${origin.scheme}://${origin.host}${portPart}`;
}

/**
 * Strict "same origin" comparison defined by the HTML/URL standards:
 * scheme, host and port must all match. Shortening document.domain never
 * mutates this tuple, so the port remains authoritative here.
 */
export function isSameOrigin(a: Origin, b: Origin): boolean {
  return (
    a.scheme === b.scheme &&
    a.host === b.host &&
    a.port === b.port
  );
}
