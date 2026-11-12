/**
 * Same-site comparison without pulling in a full public-suffix list.
 * "Same site" means identical scheme and registrable domain; the small
 * multi-part suffix table covers the common exceptions and can be extended.
 */

const MULTI_PART_SUFFIXES = new Set([
  'co.uk',
  'org.uk',
  'gov.uk',
  'ac.uk',
  'me.uk',
  'ltd.uk',
  'co.jp',
  'ne.jp',
  'or.jp',
  'go.jp',
  'ac.jp',
  'co.kr',
  'com.cn',
  'net.cn',
  'org.cn',
  'gov.cn',
  'com.au',
  'net.au',
  'org.au',
  'com.br',
  'com.tw',
  'co.nz',
  'co.in',
  'com.sg',
  'com.hk',
  'com.mx',
  'com.tr',
  'co.za',
]);

/**
 * Returns the registrable (eTLD+1) domain, or null for an unparsable / opaque
 * origin. IP addresses and localhost return themselves.
 */
export function registrableDomain(origin: string): string | null {
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return null;
  }

  const host = url.hostname;
  if (host === '' || host.includes('%')) {
    return null;
  }
  if (host === 'localhost' || /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.includes(':')) {
    return host;
  }

  const labels = host.split('.');
  if (labels.length <= 2) {
    return host;
  }

  const lastTwo = labels.slice(-2).join('.');
  const lastThree = labels.slice(-3).join('.');
  if (MULTI_PART_SUFFIXES.has(lastTwo)) {
    return lastThree;
  }
  return lastTwo;
}

/**
 * Scheme plus registrable domain comparison. Opaque/unparsable origins never
 * count as same-site (fail closed).
 */
export function isSameSite(originA: string, originB: string): boolean {
  let urlA: URL;
  let urlB: URL;
  try {
    urlA = new URL(originA);
    urlB = new URL(originB);
  } catch {
    return false;
  }

  if (urlA.protocol !== urlB.protocol) {
    return false;
  }

  const domainA = registrableDomain(originA);
  const domainB = registrableDomain(originB);
  if (domainA === null || domainB === null) {
    return false;
  }
  return domainA === domainB;
}
