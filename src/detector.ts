export type VolumeType = "local" | "shared" | "snapshot";

export interface WriteEvent {
  path: string;
  volume: VolumeType;
  timestamp: number;
  previousExtension: string;
  extension: string;
  previousEntropy: number;
  entropy: number;
  processName?: string;
}

export interface DetectorConfig {
  windowMs: number;
  rewriteThreshold: number;
  extChangeThreshold: number;
  entropyJumpThreshold: number;
  entropyJumpDelta: number;
  highEntropyFloor: number;
  allowedBulkExtensions: ReadonlySet<string>;
}

export interface Verdict {
  windowMs: number;
  rewriteCount: number;
  extChangeCount: number;
  entropyJumpCount: number;
  thresholds: {
    rewrite: number;
    extChange: number;
    entropyJump: number;
  };
  alert: boolean;
  reasons: string[];
}

export const DEFAULT_CONFIG: DetectorConfig = {
  windowMs: 10_000,
  rewriteThreshold: 20,
  extChangeThreshold: 10,
  entropyJumpThreshold: 10,
  entropyJumpDelta: 1.5,
  highEntropyFloor: 7.0,
  allowedBulkExtensions: new Set([
    ".zip", ".gz", ".bz2", ".xz", ".7z", ".tar", ".rar",
    ".mp4", ".mkv", ".webm", ".mp3", ".aac", ".flac",
    ".jpg", ".png", ".webp", ".avif",
  ]),
};

function normalizeExtension(ext: string): string {
  return ext.toLowerCase();
}

export class BehaviorDetector {
  private readonly config: DetectorConfig;
  private readonly events = new Map<string, WriteEvent>();

  constructor(config: Partial<DetectorConfig> = {}) {
    this.config = { ...DEFAULT_CONFIG, ...config };
  }

  ingest(event: WriteEvent): void {
    if (event.volume === "snapshot") return;
    this.prune(event.timestamp);
    this.events.set(event.path, event);
  }

  evaluate(now: number): Verdict {
    this.prune(now);
    const { allowedBulkExtensions, entropyJumpDelta, highEntropyFloor } = this.config;

    let rewriteCount = 0;
    let extChangeCount = 0;
    let entropyJumpCount = 0;

    for (const event of this.events.values()) {
      rewriteCount++;
      const ext = normalizeExtension(event.extension);
      const prevExt = normalizeExtension(event.previousExtension);
      const excluded = allowedBulkExtensions.has(ext);

      if (!excluded && ext !== prevExt) extChangeCount++;
      if (
        !excluded &&
        event.entropy - event.previousEntropy >= entropyJumpDelta &&
        event.entropy >= highEntropyFloor
      ) {
        entropyJumpCount++;
      }
    }

    const thresholds = {
      rewrite: this.config.rewriteThreshold,
      extChange: this.config.extChangeThreshold,
      entropyJump: this.config.entropyJumpThreshold,
    };

    const reasons: string[] = [];
    if (rewriteCount >= thresholds.rewrite) {
      reasons.push(`rewrite rate ${rewriteCount}/${thresholds.rewrite} in ${this.config.windowMs}ms`);
    }
    if (extChangeCount >= thresholds.extChange) {
      reasons.push(`bulk extension change ${extChangeCount}/${thresholds.extChange}`);
    }
    if (entropyJumpCount >= thresholds.entropyJump) {
      reasons.push(`entropy jump ${entropyJumpCount}/${thresholds.entropyJump}`);
    }

    return {
      windowMs: this.config.windowMs,
      rewriteCount,
      extChangeCount,
      entropyJumpCount,
      thresholds,
      alert: reasons.length === 3,
      reasons,
    };
  }

  private prune(now: number): void {
    for (const [path, event] of this.events) {
      if (now - event.timestamp > this.config.windowMs) this.events.delete(path);
    }
  }
}
