import { CodecError } from './errors.ts';
import {
  WIRE_LEN,
  WIRE_VARINT,
  readFieldHeader,
  skipField,
  writeFieldHeader,
} from './fields.ts';
import { ByteReader } from './reader.ts';
import { ByteWriter } from './writer.ts';

export const ENTRY_NOOP = 0;
export const ENTRY_VALUE = 1;
export type EntryType = typeof ENTRY_NOOP | typeof ENTRY_VALUE;

/**
 * LogEntry payload schema (TLV, tag<<3|wire headers):
 *   v1: 1:index varint, 2:term varint, 3:nodeId varint, 4:type varint, 5:value LEN
 *   v2: 6:flags varint, 7:timestampMs varint   (absent in v1 -> defaults 0)
 * Unknown tags are skipped by wire type (forward compatibility).
 */
export interface LogEntry {
  index: number;
  term: number;
  nodeId: number;
  type: EntryType;
  value: Buffer;
  flags: number;
  timestampMs: number;
}

export function makeEntry(
  index: number,
  term: number,
  nodeId: number,
  type: EntryType,
  value: Buffer = Buffer.alloc(0),
  flags = 0,
  timestampMs = 0,
): LogEntry {
  return { index, term, nodeId, type, value, flags, timestampMs };
}

export function encodeLogEntryPayload(e: LogEntry, version: 1 | 2 = 2): Buffer {
  const w = new ByteWriter(64 + e.value.length);
  writeFieldHeader(w, 1, WIRE_VARINT);
  w.varint(e.index);
  writeFieldHeader(w, 2, WIRE_VARINT);
  w.varint(e.term);
  writeFieldHeader(w, 3, WIRE_VARINT);
  w.varint(e.nodeId);
  writeFieldHeader(w, 4, WIRE_VARINT);
  w.varint(e.type);
  writeFieldHeader(w, 5, WIRE_LEN);
  w.varint(e.value.length);
  w.bytes(e.value);
  if (version >= 2) {
    writeFieldHeader(w, 6, WIRE_VARINT);
    w.varint(e.flags);
    writeFieldHeader(w, 7, WIRE_VARINT);
    w.varint(e.timestampMs);
  }
  return w.finish();
}

/**
 * Zero-copy decode: the returned `value` is a view into `payload`'s backing
 * buffer. Use `decodeLogEntryCopy` when a detached entry is required.
 */
export function decodeLogEntryView(payload: Buffer): LogEntry {
  const r = new ByteReader(payload);
  const e = makeEntry(0, 0, 0, ENTRY_NOOP, Buffer.alloc(0), 0, 0);
  while (r.remaining > 0) {
    const { tag, wire } = readFieldHeader(r);
    switch (tag) {
      case 1:
        if (wire !== WIRE_VARINT) throw new CodecError('BAD_WIRE_TYPE', 'index must be varint');
        e.index = r.varint();
        break;
      case 2:
        if (wire !== WIRE_VARINT) throw new CodecError('BAD_WIRE_TYPE', 'term must be varint');
        e.term = r.varint();
        break;
      case 3:
        if (wire !== WIRE_VARINT) throw new CodecError('BAD_WIRE_TYPE', 'nodeId must be varint');
        e.nodeId = r.varint();
        break;
      case 4:
        if (wire !== WIRE_VARINT) throw new CodecError('BAD_WIRE_TYPE', 'type must be varint');
        e.type = r.varint() as EntryType;
        if (e.type !== ENTRY_NOOP && e.type !== ENTRY_VALUE) {
          throw new CodecError('MALFORMED', `unknown entry type ${e.type}`);
        }
        break;
      case 5: {
        if (wire !== WIRE_LEN) throw new CodecError('BAD_WIRE_TYPE', 'value must be length-delimited');
        const n = r.varint();
        e.value = r.bytesView(n);
        break;
      }
      case 6:
        if (wire !== WIRE_VARINT) throw new CodecError('BAD_WIRE_TYPE', 'flags must be varint');
        e.flags = r.varint();
        break;
      case 7:
        if (wire !== WIRE_VARINT) throw new CodecError('BAD_WIRE_TYPE', 'timestampMs must be varint');
        e.timestampMs = r.varint();
        break;
      default:
        skipField(r, wire);
    }
  }
  return e;
}

/** Detached decode: `value` is copied out of the source buffer. */
export function decodeLogEntryCopy(payload: Buffer): LogEntry {
  const e = decodeLogEntryView(payload);
  e.value = Buffer.from(e.value);
  return e;
}
