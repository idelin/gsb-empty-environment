import type { CompiledGraph } from './graph.ts';
import { Pipeline, pipelineVersion, type PipelineConfig } from './pipeline.ts';

export interface CacheLookup {
  pipeline: Pipeline;
  cacheHit: boolean;
  version: string;
}

/**
 * Pipeline cache bound to configuration versions. The cache key is a
 * content hash of (graph fingerprint + pipeline config), so a hot config
 * swap produces a fresh version while entries compiled against older
 * versions stay isolated and are never reused for a different config.
 */
export class PipelineCache {
  private readonly entries = new Map<string, Pipeline>();
  readonly graph: CompiledGraph;

  constructor(graph: CompiledGraph) {
    this.graph = graph;
  }

  versionFor(config: PipelineConfig): string {
    return pipelineVersion(this.graph, config);
  }

  getOrBuild(config: PipelineConfig): CacheLookup {
    const version = this.versionFor(config);
    const cached = this.entries.get(version);
    if (cached !== undefined) {
      return { pipeline: cached, cacheHit: true, version };
    }
    const pipeline = new Pipeline(this.graph, config);
    this.entries.set(version, pipeline);
    return { pipeline, cacheHit: false, version };
  }

  /** Lookup by exact version; entries from other versions stay isolated. */
  get(version: string): Pipeline | undefined {
    return this.entries.get(version);
  }

  has(version: string): boolean {
    return this.entries.has(version);
  }

  evict(version: string): boolean {
    return this.entries.delete(version);
  }

  get size(): number {
    return this.entries.size;
  }
}

/**
 * Facade holding the current configuration; supports hot swapping.
 * Swapping the config never mutates already-cached pipelines — the new
 * config simply resolves to a different version key.
 */
export class ColorEngine {
  readonly cache: PipelineCache;
  readonly graph: CompiledGraph;
  private config: PipelineConfig;

  constructor(graph: CompiledGraph, config: PipelineConfig) {
    this.graph = graph;
    this.cache = new PipelineCache(graph);
    this.config = config;
  }

  get currentConfig(): PipelineConfig {
    return this.config;
  }

  pipeline(): Pipeline {
    return this.cache.getOrBuild(this.config).pipeline;
  }

  /** Hot swap: subsequent pipeline() calls use the new config/version. */
  setConfig(config: PipelineConfig): Pipeline {
    this.config = config;
    return this.pipeline();
  }
}
