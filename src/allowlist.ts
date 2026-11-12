import { ImportMapError } from './errors.ts';

export interface ParsedOrigin {
  readonly origin: string;
}

export function parseAllowedOrigins(origins: readonly string[]): Set<string> {
  if (!Array.isArray(origins) || origins.length === 0) {
    throw new ImportMapError('INVALID_SHAPE', 'allowedOrigins must be a non-empty array');
  }

  const allowed = new Set<string>();
  for (const raw of origins) {
    if (typeof raw !== 'string' || raw.length === 0) {
      throw new ImportMapError('INVALID_SHAPE', 'allowedOrigins entries must be non-empty strings');
    }

    let url: URL;
    try {
      url = new URL(raw);
    } catch {
      throw new ImportMapError('INVALID_URL', `allowed origin is not a valid URL: ${raw}`);
    }

    if (url.protocol !== 'https:' && url.protocol !== 'http:') {
      throw new ImportMapError('INVALID_URL', `allowed origin must use http or https: ${raw}`);
    }
    if (url.username !== '' || url.password !== '') {
      throw new ImportMapError('INVALID_URL', `allowed origin must not contain credentials: ${raw}`);
    }
    if (url.pathname !== '' && url.pathname !== '/' || url.search !== '' || url.hash !== '') {
      throw new ImportMapError('INVALID_URL', `allowed origin must not contain a path, query or hash: ${raw}`);
    }

    allowed.add(url.origin);
  }

  return allowed;
}

export function parseHttpUrl(value: string, label: string): URL {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new ImportMapError('INVALID_URL', `${label} is not an absolute URL: ${value}`);
  }

  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new ImportMapError('INVALID_URL', `${label} must use http or https: ${value}`);
  }
  if (url.username !== '' || url.password !== '') {
    throw new ImportMapError('INVALID_URL', `${label} must not contain credentials: ${value}`);
  }

  return url;
}

export function assertAllowedOrigin(url: URL, allowedOrigins: Set<string>, label: string): void {
  if (!allowedOrigins.has(url.origin)) {
    throw new ImportMapError(
      'DISALLOWED_ORIGIN',
      `${label} origin ${url.origin} is not in the allow list`,
    );
  }
}
