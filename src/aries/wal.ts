import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { crc32, stableStringify } from './crc32.ts';
import {
  ErrorCode,
  RecordType,
  type LogRecord,
  type LogRecordPayload,
} from './types.ts';

export interface WalError {
  code: ErrorCode;
  line: number;
  message: string;
}

export interface WalReadResult {
  records: LogRecord[];
  errors: WalError[];
}

const KNOWN_TYPES = new Set<string>(Object.values(RecordType));

interface WalLine {
  crc: number;
  payload: LogRecord;
}

function encodeLine(record: LogRecord): string {
  const crc = crc32(stableStringify(record));
  const line: WalLine = { crc, payload: record };
  return JSON.stringify(line);
}

function decodeLine(line: string, lineNo: number): LogRecord {
  let parsed: WalLine;
  try {
    parsed = JSON.parse(line) as WalLine;
  } catch {
    throw { code: ErrorCode.WalCorruptRecord, line: lineNo, message: 'line is not valid JSON' } as WalError;
  }
  if (
    parsed === null ||
    typeof parsed !== 'object' ||
    typeof parsed.crc !== 'number' ||
    parsed.payload === null ||
    typeof parsed.payload !== 'object'
  ) {
    throw { code: ErrorCode.WalCorruptRecord, line: lineNo, message: 'malformed envelope' } as WalError;
  }
  const expected = crc32(stableStringify(parsed.payload));
  if (expected !== parsed.crc) {
    throw { code: ErrorCode.WalChecksumMismatch, line: lineNo, message: 'crc mismatch' } as WalError;
  }
  if (!KNOWN_TYPES.has((parsed.payload as LogRecord).type)) {
    throw { code: ErrorCode.WalUnknownType, line: lineNo, message: 'unknown record type' } as WalError;
  }
  return parsed.payload;
}

/**
 * Append-only write-ahead log. Writes are synchronous, so a record is durable
 * as soon as `append` returns (WAL is always flushed before any data page).
 */
export class WalWriter {
  private readonly path: string;
  private nextLSN: number;

  constructor(path: string) {
    this.path = path;
    if (!existsSync(path)) {
      writeFileSync(path, '');
    }
    this.nextLSN = WalWriter.scanMaxLSN(path) + 1;
  }

  private static scanMaxLSN(path: string): number {
    const { records } = readWal(path);
    let max = 0;
    for (const record of records) {
      if (record.lsn > max) max = record.lsn;
    }
    return max;
  }

  append(payload: LogRecordPayload): LogRecord {
    const record = { ...payload, lsn: this.nextLSN++ } as LogRecord;
    appendFileSync(this.path, encodeLine(record) + '\n', 'utf8');
    return record;
  }

  get currentLSN(): number {
    return this.nextLSN - 1;
  }
}

/**
 * Sequential reader. Stops at the first corrupt record (a crash can leave a
 * torn tail) and reports it; all valid records before it are returned.
 */
export function readWal(path: string): WalReadResult {
  const records: LogRecord[] = [];
  const errors: WalError[] = [];
  if (!existsSync(path)) {
    return { records, errors };
  }
  const content = readFileSync(path, 'utf8');
  const lines = content.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.trim() === '') {
      continue;
    }
    try {
      records.push(decodeLine(line, i + 1));
    } catch (err) {
      errors.push(err as WalError);
      break;
    }
  }
  return { records, errors };
}
