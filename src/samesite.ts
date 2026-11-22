/**
 * Same-site decision used by the frame trust gate.
 *
 * "Same site" follows the registrable-domain (eTLD+1) definition used by
 * Permissions Policy's same-site relaxation: same scheme plus same eTLD+1.
 */

const MULTI_LABEL_SUFFIXES = new Set([
  'co.uk',
  'org.uk',
  'ac.uk',
  'gov.uk',
  'co.jp',
  'co.kr',
  'com.au',
  'net.au',
  'org.au',
  'com.br',
  'com.cn',
  'com.tw',
  'co.nz',
  'co.in',
]);

function parseOrigin(origin: string): { scheme: string; hostname: string } | null {
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return null;
  }
  return { scheme: url.protocol.replace(/:$/, ''), hostname: url.hostname.toLowerCase() };
}

/**
 * Returns the registrable domain for a hostname. Hostnames without dots
 * (localhost, single-label intranet names) and IP literals are returned as-is;
 * every label is then significant.
 */
export function registrableDomain(hostname: string): string {
  const host = hostname.toLowerCase();
  if (!host.includes('.') || host.startsWith('[') || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)) {
    return host;
  }
  const labels = host.split('.');
  const lastTwo = labels.slice(-2).join('.');
  if (MULTI_LABEL_SUFFIXES.has(lastTwo)) {
    return labels.slice(-3).join('.');
  }
  return lastTwo;
}

/**
 * Same site = same scheme and same registrable domain, per the URL standard's
 * "schemeful same-site" concept.
 */
export function isSameSite(originA: string, originB: string): boolean {
  const a = parseOrigin(originA);
  const b = parseOrigin(originB);
  if (!a || !b) {
    return false;
  }
  if (a.scheme !== b.scheme) {
    return false;
  }
  return registrableDomain(a.hostname) === registrableDomain(b.hostname);
}
