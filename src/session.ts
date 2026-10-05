import { VersionVector } from "./version-vector";

let nextSessionSeq = 0;

/**
 * A client session. Every read response routed through the library
 * records its version into the session watermark, so subsequent reads
 * (on any node) never return an older version — monotonic reads.
 */
export class Session {
  readonly id: string;
  private readonly vector: VersionVector;

  constructor(id?: string, vector?: VersionVector) {
    this.id = id || "session-" + ++nextSessionSeq;
    this.vector = vector || new VersionVector();
  }

  /** Minimum acceptable version for the next read on a partition. */
  requiredVersion(partition: string): number {
    return this.vector.get(partition);
  }

  /** Record the version watermark carried by a read response. */
  recordResponse(partition: string, version: number): void {
    this.vector.record(partition, version);
  }

  /**
   * Merge another session (or vector) into this one, taking the
   * maximum watermark per partition.
   */
  merge(other: Session | VersionVector): void {
    this.vector.mergeFrom(other instanceof Session ? other.vector : other);
  }

  /** Defensive copy of the current watermarks. */
  watermark(): VersionVector {
    return this.vector.clone();
  }
}
