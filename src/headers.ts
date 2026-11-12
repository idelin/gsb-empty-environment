export type HeaderValue = string | readonly string[] | null | undefined;

export type HeaderBag =
  | { get(name: string): string | null }
  | Iterable<readonly [string, string]>
  | Readonly<Record<string, HeaderValue>>;

export interface StrictTransportSecurityDirective {
  maxAge: number;
  includeSubDomains: boolean;
  preload: boolean;
}

export function normalizeHostname(value: string): string {
  let host = value.trim();
  const hasScheme = /^[a-z][a-z\d+.-]*:\/\//i.test(host);

  if (hasScheme) {
    try {
      host = new URL(host).hostname;
    } catch {
      host = host.replace(/^[a-z][a-z\d+.-]*:\/\//i, '');
    }
  } else if (host.startsWith('//')) {
    try {
      host = new URL(`https:${host}`).hostname;
    } catch {
      host = host.slice(2);
    }
  } else {
    host = host.split(/[/?#]/, 1)[0] ?? '';
    const at = host.lastIndexOf('@');
    if (at !== -1) {
      host = host.slice(at + 1);
    }
    if (host.startsWith('[')) {
      const closingBracket = host.indexOf(']');
      if (closingBracket !== -1) {
        host = host.slice(0, closingBracket + 1);
      }
    } else {
      const firstColon = host.indexOf(':');
      const lastColon = host.lastIndexOf(':');
      if (firstColon !== -1 && firstColon === lastColon) {
        host = host.slice(0, firstColon);
      }
    }
  }

  host = host.toLowerCase();
  if (host.endsWith('.')) {
    host = host.slice(0, -1);
  }
  return host;
}

export function resolveRequestHost(value: string | URL): {
  host: string;
  secure: boolean;
} {
  if (value instanceof URL) {
    return {
      host: normalizeHostname(value.hostname),
      secure: value.protocol === 'https:' || value.protocol === 'wss:',
    };
  }

  const input = value.trim();
  const hasScheme = /^[a-z][a-z\d+.-]*:\/\//i.test(input);
  const url = new URL(hasScheme ? input : `https://${input}`);
  return {
    host: normalizeHostname(url.hostname),
    secure: url.protocol === 'https:' || url.protocol === 'wss:',
  };
}

export function collectHeaderValues(
  headers: HeaderBag | undefined,
  name: string,
): string[] {
  if (!headers) {
    return [];
  }

  const lowerName = name.toLowerCase();
  const candidate = headers as {
    get?: (headerName: string) => string | null;
  };

  if (typeof candidate.get === 'function') {
    const value = candidate.get(name) ?? candidate.get(lowerName);
    return value == null ? [] : [value];
  }

  if (Array.isArray(headers)) {
    const values: string[] = [];
    for (const entry of headers as readonly (readonly [string, string])[]) {
      if (
        Array.isArray(entry) &&
        entry.length >= 2 &&
        String(entry[0]).toLowerCase() === lowerName
      ) {
        values.push(String(entry[1]));
      }
    }
    return values;
  }

  if (
    typeof (headers as { [Symbol.iterator]?: unknown })[Symbol.iterator] ===
    'function'
  ) {
    const values: string[] = [];
    for (const entry of headers as Iterable<readonly [string, string]>) {
      if (
        Array.isArray(entry) &&
        entry.length >= 2 &&
        String(entry[0]).toLowerCase() === lowerName
      ) {
        values.push(String(entry[1]));
      }
    }
    return values;
  }

  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() !== lowerName || value == null) {
      continue;
    }
    if (Array.isArray(value)) {
      return value.map(String);
    }
    return [String(value)];
  }

  return [];
}

export function hasHeader(
  headers: HeaderBag | undefined,
  name: string,
): boolean {
  return collectHeaderValues(headers, name).length > 0;
}

export function parseStrictTransportSecurity(
  headerValue: string,
): StrictTransportSecurityDirective | null {
  const parts = headerValue.split(';');
  const directive = parts.shift()?.trim() ?? '';
  const match = /^max-age\s*=\s*(?:"([^"]*)"|([^\s;]+))$/i.exec(directive);

  if (!match) {
    return null;
  }

  const maxAgeText = (match[1] ?? match[2] ?? '').trim();
  if (!/^\d+$/.test(maxAgeText)) {
    return null;
  }

  const maxAge = Number(maxAgeText);
  if (!Number.isSafeInteger(maxAge)) {
    return null;
  }

  let includeSubDomains = false;
  let preload = false;

  for (const part of parts) {
    const token = part.trim().toLowerCase();
    if (token === 'includesubdomains') {
      includeSubDomains = true;
    } else if (token === 'preload') {
      preload = true;
    }
  }

  return { maxAge, includeSubDomains, preload };
}

export function parseFirstStrictTransportSecurity(
  headerValues: readonly string[],
): StrictTransportSecurityDirective | null {
  for (const value of headerValues) {
    const parsed = parseStrictTransportSecurity(value);
    if (parsed) {
      return parsed;
    }
  }
  return null;
}
