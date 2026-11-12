import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';

import { normalizeHostname } from './headers.ts';

export interface LegacyPinRecord {
  host: string;
  pins: string[];
  includeSubDomains?: boolean;
  reportOnly?: boolean;
  reportUri?: string;
  source?: string;
  kind?: string;
  type?: string;
  [key: string]: unknown;
}

export interface LegacyPinSnapshot {
  filePath: string;
  existed: boolean;
  corrupt: boolean;
  records: LegacyPinRecord[];
  dynamicCount: number;
  staticCount: number;
  error?: string;
}

export interface LegacyPinPurgeResult {
  filePath: string;
  existed: boolean;
  removedDynamic: number;
  retainedStatic: number;
  rewritten: boolean;
  corrupt?: boolean;
  error?: string;
}

const CONTAINER_KEYS = new Set([
  'pins',
  'records',
  'hosts',
  'entries',
  'hpkp',
  'hostPins',
  'dynamicPins',
  'dynamic_pins',
  'staticPins',
  'static_pins',
]);

const STATIC_MARKERS = new Set([
  'static',
  'compiled',
  'compiled-in',
  'caller',
  'config',
  'configured',
  'builtin',
  'built-in',
  'managed',
]);

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return (
    value !== null &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    !(value instanceof Uint8Array)
  );
}

function hostFromObject(value: Record<string, unknown>): string | null {
  for (const key of ['host', 'hostname', 'domain', 'origin']) {
    if (typeof value[key] === 'string' && value[key].trim() !== '') {
      return normalizeHostname(value[key]);
    }
  }
  return null;
}

function pinsFromObject(value: Record<string, unknown>): string[] | null {
  for (const key of [
    'pins',
    'pin',
    'hash',
    'hashes',
    'pinSha256',
    'pin_sha256',
    'spkiSha256',
    'spki_sha256',
    'publicKeyHashes',
    'spkiHashes',
    'pin-sha256',
    'sha256',
  ]) {
    const candidate = value[key];
    if (typeof candidate === 'string' && candidate.trim() !== '') {
      return [candidate];
    }
    if (Array.isArray(candidate) && candidate.length > 0) {
      const pins = candidate.filter(
        (pin): pin is string => typeof pin === 'string' && pin.trim() !== '',
      );
      if (pins.length > 0) {
        return pins;
      }
    }
  }
  return null;
}

function recordFromObject(
  hostHint: string | null,
  value: Record<string, unknown>,
): LegacyPinRecord | null {
  const host = hostFromObject(value) ?? hostHint;
  const pins = pinsFromObject(value);
  if (!host || !pins) {
    return null;
  }

  const record: LegacyPinRecord = {
    ...value,
    host: normalizeHostname(host),
    pins,
  };

  if (
    record.includeSubDomains == null &&
    typeof value.include_subdomains === 'boolean'
  ) {
    record.includeSubDomains = value.include_subdomains;
  }
  if (record.reportUri == null && typeof value.report_uri === 'string') {
    record.reportUri = value.report_uri;
  }

  return record;
}

function recordsFromArray(value: readonly unknown[]): LegacyPinRecord[] {
  const records: LegacyPinRecord[] = [];
  for (const entry of value) {
    if (!isPlainObject(entry)) {
      continue;
    }
    const record = recordFromObject(null, entry);
    if (record) {
      records.push(record);
    }
  }
  return records;
}

function recordFromHostValue(
  host: string,
  value: unknown,
): LegacyPinRecord | null {
  if (typeof value === 'string' && value.trim() !== '') {
    return {
      host: normalizeHostname(host),
      pins: [value],
    };
  }

  if (Array.isArray(value)) {
    const pins = value.filter(
      (pin): pin is string => typeof pin === 'string' && pin.trim() !== '',
    );
    if (pins.length === 0) {
      return null;
    }
    return {
      host: normalizeHostname(host),
      pins,
    };
  }

  if (isPlainObject(value)) {
    return recordFromObject(normalizeHostname(host), value);
  }

  return null;
}

function recordsFromHostMap(value: Record<string, unknown>): LegacyPinRecord[] {
  const records: LegacyPinRecord[] = [];
  for (const [host, entry] of Object.entries(value)) {
    if (
      CONTAINER_KEYS.has(host) ||
      (!host.includes('.') && !host.includes(':'))
    ) {
      continue;
    }
    const record = recordFromHostValue(host, entry);
    if (record) {
      records.push(record);
    }
  }
  return records;
}

function markSource(
  records: LegacyPinRecord[],
  source: 'static' | 'dynamic',
): LegacyPinRecord[] {
  return records.map((record) =>
    record.source == null ? { ...record, source } : record,
  );
}

function recordsFromContainer(
  key: string,
  value: unknown,
): LegacyPinRecord[] {
  let records: LegacyPinRecord[] = [];

  if (Array.isArray(value)) {
    records = recordsFromArray(value);
  } else if (isPlainObject(value)) {
    const single = hostFromObject(value) ? recordFromObject(null, value) : null;
    records = single ? [single] : recordsFromHostMap(value);
  }

  if (key === 'dynamicPins' || key === 'dynamic_pins' || key === 'hpkp') {
    return markSource(records, 'dynamic');
  }
  if (key === 'staticPins' || key === 'static_pins') {
    return markSource(records, 'static');
  }
  return records;
}

function extractRecords(data: unknown): LegacyPinRecord[] {
  if (Array.isArray(data)) {
    return recordsFromArray(data);
  }
  if (!isPlainObject(data)) {
    return [];
  }

  if (hostFromObject(data)) {
    const single = recordFromObject(null, data);
    if (single) {
      return [single];
    }
  }

  const records: LegacyPinRecord[] = [];
  for (const [key, value] of Object.entries(data)) {
    if (CONTAINER_KEYS.has(key)) {
      records.push(...recordsFromContainer(key, value));
    }
  }

  if (records.length === 0) {
    records.push(...recordsFromHostMap(data));
  }

  return records;
}

function isStaticRecord(record: LegacyPinRecord): boolean {
  if (
    record.static === true ||
    record.compiled === true ||
    record.compiledIn === true
  ) {
    return true;
  }

  for (const field of [
    'source',
    'kind',
    'type',
    'origin',
    'mode',
    'storage',
    'managedBy',
  ]) {
    const value = record[field];
    if (typeof value === 'string' && STATIC_MARKERS.has(value.toLowerCase())) {
      return true;
    }
  }

  return false;
}

function writeSnapshot(filePath: string, records: LegacyPinRecord[]): void {
  mkdirSync(dirname(filePath), { recursive: true });
  const payload = {
    version: 2,
    pins: records,
    updatedAt: new Date().toISOString(),
  };
  writeFileSync(filePath, `${JSON.stringify(payload, null, 2)}\n`, 'utf8');
}

export class LegacyPinStore {
  readonly filePath: string;

  constructor(filePath: string) {
    if (typeof filePath !== 'string' || filePath.trim() === '') {
      throw new TypeError('LegacyPinStore requires a file path');
    }
    this.filePath = filePath;
  }

  readAll(): LegacyPinSnapshot {
    const empty: LegacyPinSnapshot = {
      filePath: this.filePath,
      existed: false,
      corrupt: false,
      records: [],
      dynamicCount: 0,
      staticCount: 0,
    };

    if (!existsSync(this.filePath)) {
      return empty;
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(readFileSync(this.filePath, 'utf8'));
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return {
        ...empty,
        existed: true,
        corrupt: true,
        error: message,
      };
    }

    const records = extractRecords(parsed);
    const staticCount = records.filter(isStaticRecord).length;
    return {
      filePath: this.filePath,
      existed: true,
      corrupt: false,
      records,
      dynamicCount: records.length - staticCount,
      staticCount,
    };
  }

  purgeDynamicPinsAtStartup(): LegacyPinPurgeResult {
    const snapshot = this.readAll();
    const base = {
      filePath: this.filePath,
      existed: snapshot.existed,
    };

    if (!snapshot.existed) {
      return {
        ...base,
        removedDynamic: 0,
        retainedStatic: 0,
        rewritten: false,
      };
    }

    if (snapshot.error && !snapshot.corrupt) {
      return {
        ...base,
        removedDynamic: 0,
        retainedStatic: 0,
        rewritten: false,
        error: snapshot.error,
      };
    }

    if (snapshot.corrupt) {
      try {
        writeSnapshot(this.filePath, []);
      } catch (error) {
        return {
          ...base,
          removedDynamic: 0,
          retainedStatic: 0,
          rewritten: false,
          corrupt: true,
          error: error instanceof Error ? error.message : String(error),
        };
      }
      return {
        ...base,
        removedDynamic: 0,
        retainedStatic: 0,
        rewritten: true,
        corrupt: true,
      };
    }

    const retained = snapshot.records.filter(isStaticRecord);
    const removedDynamic = snapshot.records.length - retained.length;

    if (removedDynamic > 0) {
      try {
        writeSnapshot(this.filePath, retained);
      } catch (error) {
        return {
          ...base,
          removedDynamic,
          retainedStatic: retained.length,
          rewritten: false,
          error: error instanceof Error ? error.message : String(error),
        };
      }
    }

    return {
      ...base,
      removedDynamic,
      retainedStatic: retained.length,
      rewritten: removedDynamic > 0,
    };
  }

  purgeDynamicPins(): LegacyPinPurgeResult {
    return this.purgeDynamicPinsAtStartup();
  }

  purgeAtStartup(): LegacyPinPurgeResult {
    return this.purgeDynamicPinsAtStartup();
  }

  clearDynamicPins(): LegacyPinPurgeResult {
    return this.purgeDynamicPinsAtStartup();
  }
}

export function purgeLegacyDynamicPinsAtStartup(
  filePath: string,
): LegacyPinPurgeResult {
  return new LegacyPinStore(filePath).purgeDynamicPinsAtStartup();
}

export const purgeLegacyDynamicPins = purgeLegacyDynamicPinsAtStartup;
