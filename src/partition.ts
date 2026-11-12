export interface Origin {
  scheme: string;
  host: string;
}

export interface TopLevelSite {
  siteHost: string;
}

export type PartitionKey = string;

export interface StoragePartition {
  origin: Origin;
  topLevelSite: TopLevelSite;
  key: PartitionKey;
}

function isHost(host: string): boolean {
  return host.length > 0 && /^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(host);
}

export function parseOrigin(value: string): Origin {
  const match = /^([a-z][a-z0-9+.-]*):\/\/([a-z0-9.-]+)(?::\d+)?(?:\/.*)?$/i.exec(value);
  if (!match) {
    throw new TypeError(`invalid origin: ${value}`);
  }
  const scheme = match[1].toLowerCase();
  const host = match[2].toLowerCase();
  if (!isHost(host)) {
    throw new TypeError(`invalid host in origin: ${value}`);
  }
  return { scheme, host };
}

export function siteOf(host: string): string {
  const labels = host.split(".");
  if (labels.length <= 2) {
    return host;
  }
  return labels.slice(-2).join(".");
}

export function parseTopLevelSite(value: string): TopLevelSite {
  const host = value.includes("://") ? parseOrigin(value).host : value.toLowerCase();
  if (!isHost(host)) {
    throw new TypeError(`invalid top-level site: ${value}`);
  }
  return { siteHost: siteOf(host) };
}

export function storagePartition(originValue: string, topLevelSiteValue: string): StoragePartition {
  const origin = parseOrigin(originValue);
  const topLevelSite = parseTopLevelSite(topLevelSiteValue);
  return {
    origin,
    topLevelSite,
    key: `${origin.scheme}://${origin.host}\0${topLevelSite.siteHost}`,
  };
}

export function firstPartyPartition(originValue: string): StoragePartition {
  const origin = parseOrigin(originValue);
  return {
    origin,
    topLevelSite: { siteHost: siteOf(origin.host) },
    key: `${origin.scheme}://${origin.host}\0${siteOf(origin.host)}`,
  };
}

export function samePartition(a: StoragePartition, b: StoragePartition): boolean {
  return a.key === b.key;
}
