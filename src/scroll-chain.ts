/**
 * Layered scroll model.
 *
 * Displacement is offered to the innermost layer first. Each layer clamps the
 * offered delta into its own [min, max] bounds, consumes what fits, and hands
 * only the out-of-bounds remainder to its ancestor. A layer with
 * `blocksChain` (overscroll-behavior: contain) swallows the remainder instead
 * of propagating it -- even a zero-range layer (min === max) still blocks.
 */

export interface ScrollLayerOptions {
  min?: number;
  max?: number;
  offset?: number;
  blocksChain?: boolean;
  name?: string;
}

export interface ConsumeResult {
  /** Delta offered to this layer. */
  offered: number;
  /** Portion actually applied to this layer's offset. */
  consumed: number;
  /** Out-of-bounds portion passed to the ancestor (0 when blocking). */
  remainder: number;
  /** Out-of-bounds portion discarded because this layer blocks the chain. */
  dropped: number;
}

export class ScrollLayer {
  min: number;
  max: number;
  offset: number;
  blocksChain: boolean;
  readonly name: string;

  constructor(options: ScrollLayerOptions = {}) {
    this.min = options.min ?? 0;
    this.max = options.max ?? 0;
    this.offset = options.offset ?? this.min;
    this.blocksChain = options.blocksChain ?? false;
    this.name = options.name ?? "layer";
  }

  get range(): number {
    return this.max - this.min;
  }

  clampToBounds(value: number): number {
    return Math.min(this.max, Math.max(this.min, value));
  }

  /**
   * Offer a displacement to this layer.
   * Invariant: offered === consumed + remainder + dropped, and at most one of
   * remainder/dropped is non-zero.
   */
  consume(delta: number): ConsumeResult {
    const offered = delta;
    const next = this.clampToBounds(this.offset + delta);
    const consumed = next - this.offset;
    this.offset = next;
    const overflow = offered - consumed;
    const remainder = this.blocksChain ? 0 : overflow;
    const dropped = this.blocksChain ? overflow : 0;
    return { offered, consumed, remainder, dropped };
  }

  /** Dynamic bounds change (content/viewport resize): re-clamp the offset. */
  setBounds(min: number, max: number): void {
    this.min = min;
    this.max = max;
    this.offset = this.clampToBounds(this.offset);
  }
}

export interface ZoomStep {
  layer: ScrollLayer;
  /** Anchor-preserving offset requested before clamping. */
  desiredOffset: number;
  result: ConsumeResult;
}

export class ScrollChain {
  /** Layers ordered innermost first, outermost (root scroller) last. */
  readonly layers: ScrollLayer[];

  constructor(layers: ScrollLayer[]) {
    if (layers.length === 0) {
      throw new Error("ScrollChain needs at least one layer");
    }
    this.layers = layers;
  }

  get innermost(): ScrollLayer {
    return this.layers[0]!;
  }

  get outermost(): ScrollLayer {
    return this.layers[this.layers.length - 1]!;
  }

  /**
   * Route a displacement through the chain, innermost to outermost.
   * Returns one ConsumeResult per layer that was offered a non-zero delta,
   * plus the final remainder that escaped the root scroller.
   */
  scroll(delta: number): { steps: ConsumeResult[]; escaped: number } {
    const steps: ConsumeResult[] = [];
    let remaining = delta;
    for (const layer of this.layers) {
      if (remaining === 0) break;
      const result = layer.consume(remaining);
      steps.push(result);
      remaining = result.remainder;
    }
    return { steps, escaped: remaining };
  }

  /**
   * Zoom by `factor` around `anchor` (viewport coordinate).
   *
   * Phase 1 (anchor): compute the offset that keeps the content point under
   * the anchor fixed: desired = (offset + anchor) * factor - anchor.
   * Phase 2 (displacement): apply each layer's desired delta innermost-first;
   * clamp overflow propagates upward like a normal scroll, subject to each
   * layer's blocksChain flag.
   */
  applyZoom(factor: number, anchor: number): ZoomStep[] {
    const desired = this.layers.map(
      (layer) => (layer.offset + anchor) * factor - anchor,
    );
    const steps: ZoomStep[] = [];
    let carried = 0;
    for (let i = 0; i < this.layers.length; i++) {
      const layer = this.layers[i]!;
      const result = layer.consume(desired[i]! - layer.offset + carried);
      steps.push({ layer, desiredOffset: desired[i]!, result });
      carried = result.remainder;
    }
    return steps;
  }
}
