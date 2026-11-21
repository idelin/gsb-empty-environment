export interface ForwardedInfo {
  host?: string;
  proto?: string;
  origin?: string;
}

function firstHeaderValue(
  headers: Record<string, string | undefined>,
  name: string,
): string | undefined {
  const value = headers[name.toLowerCase()];
  if (typeof value !== 'string') {
    return undefined;
  }
  return value.split(',')[0]?.trim() || undefined;
}

export function parseForwardedHeader(header: string): ForwardedInfo[] {
  return header
    .split(',')
    .map((directive) => directive.trim())
    .filter((directive) => directive.length > 0)
    .map((directive) => {
      const info: ForwardedInfo = {};
      for (const pair of directive.split(';')) {
        const separator = pair.indexOf('=');
        if (separator === -1) {
          continue;
        }
        const key = pair.slice(0, separator).trim().toLowerCase();
        let value = pair.slice(separator + 1).trim();
        if (
          (value.startsWith('"') && value.endsWith('"')) ||
          (value.startsWith("'") && value.endsWith("'"))
        ) {
          value = value.slice(1, -1);
        }
        if (key === 'host') {
          info.host = value;
        } else if (key === 'proto') {
          info.proto = value;
        } else if (key === 'for') {
          info.origin = value;
        }
      }
      return info;
    });
}

export function extractForwardedInfo(
  headers: Record<string, string | undefined>,
): ForwardedInfo | null {
  const standard = headers['forwarded'];
  if (typeof standard === 'string' && standard.trim().length > 0) {
    const [first] = parseForwardedHeader(standard);
    return first ?? null;
  }
  const host = firstHeaderValue(headers, 'x-forwarded-host');
  const proto = firstHeaderValue(headers, 'x-forwarded-proto');
  if (host || proto) {
    return { host, proto };
  }
  return null;
}
