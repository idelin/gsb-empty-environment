import {
  cellIndexKey,
  pseudonymFor,
  seal,
  unseal,
  type KeyHierarchy,
  type SealedRecord,
} from "./crypto.ts";
import { cellIdFor } from "./grid.ts";

export interface LocationPoint {
  entityId: string;
  lat: number;
  lon: number;
  timestamp: number;
}

export interface TrajectoryPoint {
  lat: number;
  lon: number;
  timestamp: number;
}

export interface HeatCell {
  cellId: string;
  count: number;
}

export interface StoreOptions {
  bucketSizeMs?: number;
  finePrecision?: number;
  coarsePrecision?: number;
  kAnonymity?: number;
}

interface StoredRecord {
  bucket: number;
  pseudonym: string;
  cellKey: string;
  coarseCell: string;
  sealed: SealedRecord;
  aad: string;
}

interface RecordPayload {
  entityId: string;
  lat: number;
  lon: number;
  timestamp: number;
}

const DEFAULT_BUCKET_SIZE_MS = 60 * 60 * 1000;

export class TrajectoryIndex {
  private readonly keys: KeyHierarchy;
  private readonly bucketSizeMs: number;
  private readonly finePrecision: number;
  private readonly coarsePrecision: number;
  private readonly kAnonymity: number;
  private readonly records: StoredRecord[] = [];
  private readonly byPseudonym = new Map<string, StoredRecord[]>();
  private readonly heat = new Map<string, number>();

  constructor(keys: KeyHierarchy, options: StoreOptions = {}) {
    this.keys = keys;
    this.bucketSizeMs = options.bucketSizeMs ?? DEFAULT_BUCKET_SIZE_MS;
    this.finePrecision = options.finePrecision ?? 3;
    this.coarsePrecision = options.coarsePrecision ?? 1;
    this.kAnonymity = options.kAnonymity ?? 5;
  }

  get size(): number {
    return this.records.length;
  }

  bucketOf(timestamp: number): number {
    return Math.floor(timestamp / this.bucketSizeMs);
  }

  ingest(point: LocationPoint): void {
    const bucket = this.bucketOf(point.timestamp);
    const pseudonym = pseudonymFor(this.keys.pseudonymKey, point.entityId, bucket);
    const fineCell = cellIdFor(point.lat, point.lon, this.finePrecision);
    const coarseCell = cellIdFor(point.lat, point.lon, this.coarsePrecision);
    const cellKey = cellIndexKey(this.keys.gridSalt, fineCell, bucket);
    const aad = `${bucket}|${pseudonym}|${cellKey}`;
    const payload: RecordPayload = {
      entityId: point.entityId,
      lat: point.lat,
      lon: point.lon,
      timestamp: point.timestamp,
    };
    const record: StoredRecord = {
      bucket,
      pseudonym,
      cellKey,
      coarseCell,
      sealed: seal(this.keys.recordKey, JSON.stringify(payload), aad),
      aad,
    };
    this.records.push(record);
    const lookupKey = `${bucket}|${pseudonym}`;
    const list = this.byPseudonym.get(lookupKey);
    if (list) {
      list.push(record);
    } else {
      this.byPseudonym.set(lookupKey, [record]);
    }
    const heatKey = `${bucket}|${coarseCell}`;
    this.heat.set(heatKey, (this.heat.get(heatKey) ?? 0) + 1);
  }

  heatmap(from: number, to: number): HeatCell[] {
    const fromBucket = this.bucketOf(from);
    const toBucket = this.bucketOf(to);
    const totals = new Map<string, number>();
    for (const [key, count] of this.heat) {
      const sep = key.indexOf("|");
      const bucket = Number(key.slice(0, sep));
      if (bucket < fromBucket || bucket > toBucket) continue;
      const cell = key.slice(sep + 1);
      totals.set(cell, (totals.get(cell) ?? 0) + count);
    }
    return [...totals.entries()]
      .filter(([, count]) => count >= this.kAnonymity)
      .map(([cellId, count]) => ({ cellId, count }))
      .sort((a, b) => b.count - a.count || a.cellId.localeCompare(b.cellId));
  }

  reveal(entityId: string, from: number, to: number): TrajectoryPoint[] {
    const fromBucket = this.bucketOf(from);
    const toBucket = this.bucketOf(to);
    const points: TrajectoryPoint[] = [];
    for (let bucket = fromBucket; bucket <= toBucket; bucket++) {
      const pseudonym = pseudonymFor(this.keys.pseudonymKey, entityId, bucket);
      const matches = this.byPseudonym.get(`${bucket}|${pseudonym}`);
      if (!matches) continue;
      for (const record of matches) {
        const payload = JSON.parse(
          unseal(this.keys.recordKey, record.sealed, record.aad),
        ) as RecordPayload;
        if (payload.entityId !== entityId) continue;
        if (payload.timestamp < from || payload.timestamp > to) continue;
        points.push({
          lat: payload.lat,
          lon: payload.lon,
          timestamp: payload.timestamp,
        });
      }
    }
    return points.sort((a, b) => a.timestamp - b.timestamp);
  }

  inspectPseudonymOf(entityId: string, timestamp: number): string {
    return pseudonymFor(
      this.keys.pseudonymKey,
      entityId,
      this.bucketOf(timestamp),
    );
  }
}
