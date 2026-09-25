import { mat3Apply, type Mat3, type Vec3 } from './matrix.ts';
import type { CompiledGraph } from './graph.ts';
import type { ColorSpaceDef } from './spaces.ts';
import { hashVersion, stableStringify } from './version.ts';

export type GamutStrategy = 'clip' | 'preserve';

export interface PipelineConfig {
  source: string;
  target: string;
  strategy: GamutStrategy;
  /** Optional user tag; participates in the cache version. */
  versionTag?: string;
}

export interface PipelineStep {
  kind: 'decode' | 'matrix' | 'adapt' | 'encode';
  detail: string;
}

export interface PixelResult {
  rgb: Vec3;
  clipped: boolean;
}

export class PipelineError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PipelineError';
  }
}

export const CLIP_EPSILON = 1e-9;

export function pipelineVersion(graph: CompiledGraph, config: PipelineConfig): string {
  return hashVersion(
    stableStringify({
      graph: graph.fingerprint,
      source: config.source,
      target: config.target,
      strategy: config.strategy,
      versionTag: config.versionTag ?? null,
    }),
  );
}

/**
 * A compiled conversion between two color spaces. The linear stage
 * (matrices + chromatic adaptation) is composed into a single matrix,
 * while the transfer functions stay as separate encode/decode steps.
 * When the two spaces use different white points an 'adapt' step is
 * always present — adaptation is never silently omitted.
 */
export class Pipeline {
  readonly steps: PipelineStep[] = [];
  readonly version: string;
  readonly sourceSpace: ColorSpaceDef;
  readonly targetSpace: ColorSpaceDef;
  readonly graph: CompiledGraph;
  readonly config: PipelineConfig;
  private readonly linear: Mat3;
  private reversePipeline: Pipeline | null = null;

  constructor(graph: CompiledGraph, config: PipelineConfig) {
    this.graph = graph;
    this.config = config;
    const source = graph.spaces.get(config.source);
    const target = graph.spaces.get(config.target);
    if (!source) throw new PipelineError(`unknown source space: ${config.source}`);
    if (!target) throw new PipelineError(`unknown target space: ${config.target}`);
    this.sourceSpace = source;
    this.targetSpace = target;

    this.steps.push({ kind: 'decode', detail: `${source.transfer.id} decode (${source.id})` });

    const explicit = graph.findEdge(config.source, config.target);
    if (explicit) {
      this.linear = explicit.matrix;
      this.steps.push({ kind: 'matrix', detail: explicit.label ?? `explicit edge ${config.source} -> ${config.target}` });
    } else {
      this.linear = graph.hubMatrix(config.source, config.target);
      this.steps.push({ kind: 'matrix', detail: `${config.source} linear RGB -> XYZ` });
      if (graph.requiresAdaptation(config.source, config.target)) {
        this.steps.push({
          kind: 'adapt',
          detail: `chromatic adaptation ${source.white.name} -> ${target.white.name} (Bradford)`,
        });
      }
      this.steps.push({ kind: 'matrix', detail: `XYZ -> ${config.target} linear RGB` });
    }

    this.steps.push({ kind: 'encode', detail: `${target.transfer.id} encode (${target.id})` });
    this.version = pipelineVersion(graph, config);
  }

  convertPixel(r: number, g: number, b: number): PixelResult {
    const decode = this.sourceSpace.transfer.decode;
    const encode = this.targetSpace.transfer.encode;
    const linearIn: Vec3 = [decode(r), decode(g), decode(b)];
    const linearOut = mat3Apply(this.linear, linearIn);
    let out: Vec3 = [encode(linearOut[0]), encode(linearOut[1]), encode(linearOut[2])];
    let clipped = false;
    for (const v of out) {
      if (!Number.isFinite(v)) {
        throw new PipelineError(`conversion produced a non-finite component in ${this.config.source} -> ${this.config.target}`);
      }
    }
    if (this.config.strategy === 'clip') {
      out = out.map((v) => {
        if (v < -CLIP_EPSILON || v > 1 + CLIP_EPSILON) clipped = true;
        return v < 0 ? 0 : v > 1 ? 1 : v;
      }) as unknown as Vec3;
    }
    return { rgb: out, clipped };
  }

  /**
   * Lazily built reverse conversion. Always uses the 'preserve' strategy
   * so round-trip measurements reflect the forward pass honestly instead
   * of hiding error behind a second clip.
   */
  reverse(): Pipeline {
    if (this.reversePipeline === null) {
      this.reversePipeline = new Pipeline(this.graph, {
        source: this.config.target,
        target: this.config.source,
        strategy: 'preserve',
      });
    }
    return this.reversePipeline;
  }

  describe(): string {
    return `${this.config.source} -> ${this.config.target} [${this.config.strategy}] v${this.version}\n` +
      this.steps.map((s) => `  - ${s.kind}: ${s.detail}`).join('\n');
  }
}
