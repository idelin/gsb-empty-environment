/** ARIES log record types. */
export const LogType = {
  UPDATE: "UPDATE",
  COMMIT: "COMMIT",
  ABORT: "ABORT",
  END: "END",
  CLR: "CLR",
  BEGIN_CHECKPOINT: "BEGIN_CHECKPOINT",
  END_CHECKPOINT: "END_CHECKPOINT",
} as const;
export type LogType = (typeof LogType)[keyof typeof LogType];

export type TxnStatus = "RUNNING" | "COMMITTED" | "ABORTING";

export interface CheckpointTTEntry {
  txnId: number;
  status: TxnStatus;
  lastLSN: number;
}

export interface LogRecord {
  lsn: number;
  prevLSN: number;
  txnId: number; // -1 for checkpoint records
  type: LogType;
  pageId?: number;
  undoNextLSN?: number; // CLR only
  beforeImage?: string;
  afterImage?: string;
  /** END_CHECKPOINT payload: snapshot of DPT (pageId -> recLSN) and TT. */
  checkpointDPT?: Record<string, number>;
  checkpointTT?: CheckpointTTEntry[];
}

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  return table;
})();

export function crc32(buf: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < buf.length; i++) crc = CRC_TABLE[(crc ^ buf[i]!) & 0xff]! ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

export function encodeRecord(record: LogRecord): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(record));
}

export function decodeRecord(buf: Uint8Array): LogRecord {
  return JSON.parse(new TextDecoder().decode(buf)) as LogRecord;
}
