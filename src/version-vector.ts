/**
 * VersionVector tracks the highest version seen per partition.
 * It is the "watermark" structure behind session guarantees:
 * a read is only acceptable if its version is >= the watermark
 * recorded for that partition.
 */
export class VersionVector {
  private readonly versions: Map<string, number>;

  constructor(entries?: ReadonlyArray<readonly [string, number]>) {
    this.versions = new Map<string, number>(entries);
  }

  /** Highest version recorded for a partition (0 if never seen). */
  get(partition: string): number {
    return this.versions.get(partition) || 0;
  }

  /**
   * Record an observed version. Never lowers the watermark:
   * only a strictly newer version replaces the stored one.
   */
  record(partition: string, version: number): void {
    if (version > this.get(partition)) {
      this.versions.set(partition, version);
    }
  }

  /**
   * Merge another vector into this one, taking the maximum
   * version for every partition (session merge semantics).
   */
  mergeFrom(other: VersionVector): void {
    for (const [partition, version] of other.entries()) {
      this.record(partition, version);
    }
  }

  /** Returns a new vector holding the per-partition max of both inputs. */
  static merge(a: VersionVector, b: VersionVector): VersionVector {
    const merged = a.clone();
    merged.mergeFrom(b);
    return merged;
  }

  clone(): VersionVector {
    return new VersionVector(this.entries());
  }

  entries(): Array<readonly [string, number]> {
    return Array.from(this.versions.entries());
  }
}
