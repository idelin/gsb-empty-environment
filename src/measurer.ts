import { GenerationCache } from "./cache.ts";
import { ESTIMATE_FACE } from "./fixtures.ts";
import type { ControllableScheduler } from "./scheduler.ts";
import {
  measureKey,
  styleKey,
  type FontFace,
  type FontVersion,
  type GlyphCluster,
  type Measurement,
  type MeasureKey,
  type TextStyle,
} from "./types.ts";

/** Actual in-memory footprint models: budget is charged by real entry size. */
export function sizeofFontFace(face: FontFace): number {
  return 96 + Object.keys(face.glyphAdvances).length * 16;
}

export function sizeofClusters(clusters: GlyphCluster[]): number {
  let size = 48;
  for (const cluster of clusters) {
    size += 32 + cluster.text.length * 2 + cluster.glyphs.length * 24;
  }
  return size;
}

export function sizeofMeasurement(measurement: Measurement): number {
  return 64 + sizeofClusters(measurement.clusters);
}

/** Registry of loaded fonts; hot reload replaces a face and bumps its version. */
export class FontRegistry {
  private faces = new Map<string, FontFace>();
  private reloadListeners: Array<(family: string, face: FontFace) => void> = [];

  current(family: string): FontFace | undefined {
    return this.faces.get(family);
  }

  versionOf(family: string): FontVersion | undefined {
    return this.faces.get(family)?.version;
  }

  register(face: FontFace): void {
    this.faces.set(face.family, face);
  }

  onReload(listener: (family: string, face: FontFace) => void): void {
    this.reloadListeners.push(listener);
  }

  /** Hot update: atomically replaces the face, then notifies synchronously. */
  hotReload(face: FontFace): void {
    this.faces.set(face.family, face);
    for (const listener of this.reloadListeners) listener(face.family, face);
  }
}

export interface MeasureHandle {
  readonly promise: Promise<Measurement>;
  /** Detach this subscriber; the shared task dies only when nobody is left. */
  cancel(): void;
}

export interface MeasureRequest {
  /** Synchronous coarse measurement, published immediately. */
  readonly estimate: Measurement;
  /** Precise measurement, published when the shared task settles. */
  readonly final: Promise<Measurement>;
  readonly handle: MeasureHandle;
}

type Listener = (key: MeasureKey, measurement: Measurement) => void;

interface InFlight {
  key: MeasureKey;
  subscribers: number;
  settled: boolean;
  cancelled: boolean;
  resolve: (value: Measurement) => void;
  reject: (reason: unknown) => void;
}

const MAX_VERSION_RETRIES = 8;

export class FontMeasurer {
  readonly registry: FontRegistry;
  private readonly scheduler: ControllableScheduler;

  /** Three measurement layers, all tagged with one shared cache generation. */
  readonly fontCache: GenerationCache<FontFace>;
  readonly clusterCache: GenerationCache<GlyphCluster[]>;
  readonly measureCache: GenerationCache<Measurement>;

  private inFlight = new Map<string, InFlight>();
  private estimateListeners: Listener[] = [];
  private finalListeners: Listener[] = [];

  readonly stats = {
    tasksStarted: 0,
    cacheHits: 0,
    subscribersMerged: 0,
    staleDiscards: 0,
    versionRetries: 0,
  };

  constructor(
    registry: FontRegistry,
    scheduler: ControllableScheduler,
    budgets: { fonts: number; clusters: number; measures: number } = {
      fonts: 4096,
      clusters: 8192,
      measures: 16384,
    },
  ) {
    this.registry = registry;
    this.scheduler = scheduler;
    this.fontCache = new GenerationCache(budgets.fonts, sizeofFontFace);
    this.clusterCache = new GenerationCache(budgets.clusters, sizeofClusters);
    this.measureCache = new GenerationCache(budgets.measures, sizeofMeasurement);
    // Font hot update: one synchronous, atomic generation switch across all
    // layers. Stale async writers then fail the generation check on `set`.
    registry.onReload(() => this.swapGeneration());
  }

  /** Atomic cache-generation switch for font hot updates. */
  swapGeneration(): void {
    this.fontCache.swapGeneration();
    this.clusterCache.swapGeneration();
    this.measureCache.swapGeneration();
  }

  onEstimate(listener: Listener): void {
    this.estimateListeners.push(listener);
  }

  onFinal(listener: Listener): void {
    this.finalListeners.push(listener);
  }

  private emit(listeners: Listener[], key: MeasureKey, measurement: Measurement): void {
    for (const listener of listeners) listener(key, measurement);
  }

  /** Synchronous estimate from coarse fixture metrics — never cached as final. */
  estimate(text: string, style: TextStyle): Measurement {
    return this.compute(text, style, ESTIMATE_FACE);
  }

  /**
   * Estimate and final are published on separate channels: the estimate is
   * returned synchronously, the final travels through the shared async task.
   */
  request(text: string, style: TextStyle): MeasureRequest {
    const estimate = this.estimate(text, style);
    const handle = this.measure(text, style);
    const key = this.currentKey(text, style);
    this.emit(this.estimateListeners, key, estimate);
    const final = handle.promise.then((measurement) => {
      this.emit(this.finalListeners, key, measurement);
      return measurement;
    });
    return { estimate, final, handle };
  }

  private currentKey(text: string, style: TextStyle): MeasureKey {
    return {
      text,
      style,
      fontVersion: this.registry.versionOf(style.fontFamily) ?? 0,
    };
  }

  /** Synchronous look at the final-measurement cache, if already settled. */
  peekFinal(text: string, style: TextStyle): Measurement | undefined {
    return this.measureCache.peek(measureKey(this.currentKey(text, style)));
  }

  /** Cached-or-shared precise measurement. */
  measure(text: string, style: TextStyle): MeasureHandle {
    const key = this.currentKey(text, style);
    const encoded = measureKey(key);

    const cached = this.measureCache.get(encoded);
    if (cached) {
      this.stats.cacheHits += 1;
      return { promise: Promise.resolve(cached), cancel: () => {} };
    }

    const existing = this.inFlight.get(encoded);
    if (existing) {
      // Same-key merge: one shared task, many waiters.
      existing.subscribers += 1;
      this.stats.subscribersMerged += 1;
      return this.subscriberHandle(encoded, existing);
    }

    const flight: InFlight = {
      key,
      subscribers: 1,
      settled: false,
      cancelled: false,
      resolve: () => {},
      reject: () => {},
    };
    const promise = new Promise<Measurement>((resolve, reject) => {
      flight.resolve = resolve;
      flight.reject = reject;
    });
    // Avoid unhandled-rejection noise when every subscriber cancels.
    promise.catch(() => {});
    this.inFlight.set(encoded, flight);
    this.stats.tasksStarted += 1;

    this.scheduler.enqueue(`measure:${encoded}`, async () => {
      if (flight.cancelled) return;
      try {
        const measurement = await this.runMeasurement(key, flight);
        if (flight.cancelled || flight.settled) return;
        flight.settled = true;
        flight.resolve(measurement);
      } catch (error) {
        if (!flight.settled) {
          flight.settled = true;
          flight.reject(error);
        }
      } finally {
        if (this.inFlight.get(encoded) === flight && flight.settled) {
          this.inFlight.delete(encoded);
        }
      }
    });

    return this.subscriberHandle(encoded, flight, promise);
  }

  private subscriberHandle(
    encoded: string,
    flight: InFlight,
    promise?: Promise<Measurement>,
  ): MeasureHandle {
    const tracked =
      promise ??
      new Promise<Measurement>((resolve, reject) => {
        const originalResolve = flight.resolve;
        const originalReject = flight.reject;
        flight.resolve = (value) => {
          originalResolve(value);
          resolve(value);
        };
        flight.reject = (reason) => {
          originalReject(reason);
          reject(reason);
        };
      });
    tracked.catch(() => {});
    let active = true;
    return {
      promise: tracked,
      cancel: () => {
        if (!active) return;
        active = false;
        flight.subscribers -= 1;
        // The shared task is cancelled only when its last waiter leaves.
        if (flight.subscribers === 0 && !flight.settled) {
          flight.cancelled = true;
          flight.settled = true;
          flight.reject(new Error("all subscribers cancelled"));
          if (this.inFlight.get(encoded) === flight) this.inFlight.delete(encoded);
        }
      },
    };
  }

  /**
   * Performs the async measurement. The font version captured at request time
   * is re-validated after every await: a late font load or a hot reload that
   * moved the version forward discards the stale result and retries against
   * the current version, so an old reply never overwrites newer data.
   */
  private async runMeasurement(key: MeasureKey, flight: InFlight): Promise<Measurement> {
    let attempt = 0;
    for (;;) {
      const font = await this.loadFont(key.style.fontFamily);
      if (flight.cancelled) throw new Error("cancelled");

      const currentVersion = this.registry.versionOf(key.style.fontFamily);
      if (currentVersion === undefined) throw new Error(`unknown font ${key.style.fontFamily}`);
      if (font.version !== currentVersion || key.fontVersion !== currentVersion) {
        // Stale reply: the world moved while we were awaiting. Retry against
        // the live version instead of publishing an outdated result.
        this.stats.staleDiscards += 1;
        attempt += 1;
        if (attempt > MAX_VERSION_RETRIES) throw new Error("font version unstable");
        this.stats.versionRetries += 1;
        key = { ...key, fontVersion: currentVersion };
        continue;
      }

      const clusters = this.clustersFor(key, font);
      const measurement = this.computeFromClusters(key.style, font, clusters);
      // Generation check inside `set`: dropped silently if a hot reload
      // swapped the cache generation while this task was in flight.
      this.measureCache.set(measureKey(key), measurement, this.measureCache.currentGeneration);
      return measurement;
    }
  }

  /**
   * Loads a font through the scheduler. A late-arriving load is validated
   * against the registry: if a hot reload already installed a newer face,
   * the registry's current face wins over the stale load reply.
   */
  private loadFont(family: string): Promise<FontFace> {
    const cached = this.fontCache.get(family);
    if (cached && cached.version === this.registry.versionOf(family)) {
      return Promise.resolve(cached);
    }
    // The backend reply captures whatever is current *now*; by the time the
    // scheduler delivers it, a hot reload may have moved the version forward.
    const reply = this.registry.current(family);
    return new Promise<FontFace>((resolve, reject) => {
      this.scheduler.enqueue(`load-font:${family}@v${reply?.version ?? "?"}`, () => {
        if (!reply) {
          reject(new Error(`unknown font ${family}`));
          return;
        }
        // Validate the late reply: only cache it while it is still current.
        if (reply.version === this.registry.versionOf(family)) {
          this.fontCache.set(family, reply, this.fontCache.currentGeneration);
        }
        resolve(reply);
      });
    });
  }

  private clustersFor(key: MeasureKey, font: FontFace): GlyphCluster[] {
    const cacheKey = `${styleKey(key.style)}|v${key.fontVersion}|${key.text}`;
    const cached = this.clusterCache.get(cacheKey);
    if (cached) return cached;
    const clusters = shape(key.text, key.style, font);
    this.clusterCache.set(cacheKey, clusters, this.clusterCache.currentGeneration);
    return clusters;
  }

  /** Pure fixture computation: advances come from data, not a layout engine. */
  private compute(text: string, style: TextStyle, font: FontFace): Measurement {
    return this.computeFromClusters(style, font, shape(text, style, font));
  }

  private computeFromClusters(
    style: TextStyle,
    font: FontFace,
    clusters: GlyphCluster[],
  ): Measurement {
    const scale = style.fontSize / font.unitsPerEm;
    const width = clusters.reduce((sum, cluster) => sum + cluster.width, 0);
    return {
      width,
      ascent: font.ascent * scale,
      descent: font.descent * scale,
      lineHeight: (font.ascent + font.descent + font.lineGap) * scale,
      clusters,
    };
  }
}

/**
 * Fixture shaper: splits text into word/space clusters and sums glyph
 * advances. letterSpacing is inserted between adjacent glyphs, so identical
 * text with different tracking necessarily produces different clusters.
 */
export function shape(text: string, style: TextStyle, font: FontFace): GlyphCluster[] {
  const scale = style.fontSize / font.unitsPerEm;
  const clusters: GlyphCluster[] = [];
  const parts = text.split(/(\s+)/).filter((part) => part.length > 0);
  for (const part of parts) {
    const glyphs = [];
    let width = 0;
    for (const ch of part) {
      const advance = font.glyphAdvances[ch] ?? font.defaultAdvance;
      const glyphWidth = advance * scale + style.letterSpacing;
      glyphs.push({ text: ch, width: glyphWidth });
      width += glyphWidth;
    }
    clusters.push({ text: part, glyphs, width });
  }
  return clusters;
}
