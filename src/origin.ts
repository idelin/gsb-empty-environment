export interface OriginTriple {
  scheme: string;
  hostname: string;
  port: number | null;
}

const DEFAULT_PORTS: Record<string, number> = {
  http: 80,
  https: 443,
  ws: 80,
  wss: 443,
};

export function parseOrigin(raw: string): OriginTriple {
  if (typeof raw !== 'string' || raw.length === 0) {
    throw new Error('origin must be a non-empty string');
  }
  const match = /^([a-zA-Z][a-zA-Z0-9+.-]*):\/\/([^/:?#]+)(?::(\d+))?\/?$/.exec(
    raw,
  );
  if (!match) {
    throw new Error(`malformed origin: ${raw}`);
  }
  const scheme = match[1].toLowerCase();
  if (!(scheme in DEFAULT_PORTS)) {
    throw new Error(`unsupported origin scheme: ${scheme}`);
  }
  const hostname = match[2].toLowerCase();
  if (hostname.length === 0) {
    throw new Error(`malformed origin host: ${raw}`);
  }
  const port = match[3] === undefined ? null : Number(match[3]);
  if (port !== null && (port < 1 || port > 65535)) {
    throw new Error(`invalid origin port: ${raw}`);
  }
  return { scheme, hostname, port };
}

export function sameOrigin(a: OriginTriple, b: OriginTriple): boolean {
  if (a.scheme !== b.scheme || a.hostname !== b.hostname) {
    return false;
  }
  const defaultPort = DEFAULT_PORTS[a.scheme];
  return (a.port ?? defaultPort) === (b.port ?? defaultPort);
}

export function originMatches(rawOrigin: string, trustedOrigin: string): boolean {
  try {
    return sameOrigin(parseOrigin(rawOrigin), parseOrigin(trustedOrigin));
  } catch {
    return false;
  }
}
