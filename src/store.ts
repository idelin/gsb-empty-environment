import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { EvidenceRecord } from './evidence.ts';

export function saveRecord(dir: string, record: EvidenceRecord): string {
  mkdirSync(dir, { recursive: true });
  const file = join(dir, `${record.recordId}.json`);
  writeFileSync(file, JSON.stringify(record, null, 2));
  return file;
}

export function loadRecord(dir: string, recordId: string): EvidenceRecord {
  const file = join(dir, `${recordId}.json`);
  return JSON.parse(readFileSync(file, 'utf8')) as EvidenceRecord;
}
