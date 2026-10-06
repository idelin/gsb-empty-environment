export interface ScrollLayer {
  position: number;
  min: number;
  max: number;
  blocksChain: boolean;
}

export interface LayerConsumption {
  input: number;
  consumed: number;
  remainder: number;
  blocked: boolean;
}

export interface ScrollChainResult {
  layers: LayerConsumption[];
  totalConsumed: number;
  overflow: number;
  discarded: number;
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

export function applyScrollDelta(
  layers: ScrollLayer[],
  delta: number,
): ScrollChainResult {
  const report: LayerConsumption[] = [];
  let remaining = delta;

  for (const layer of layers) {
    const next = clamp(layer.position + remaining, layer.min, layer.max);
    const consumed = next - layer.position;
    layer.position = next;
    const remainder = remaining - consumed;
    const blocked = layer.blocksChain;
    report.push({ input: remaining, consumed, remainder, blocked });
    remaining = remainder;
    if (blocked) {
      return {
        layers: report,
        totalConsumed: delta - remaining,
        overflow: 0,
        discarded: remaining,
      };
    }
  }

  return {
    layers: report,
    totalConsumed: delta - remaining,
    overflow: remaining,
    discarded: 0,
  };
}

export interface ScalableScrollLayer extends ScrollLayer {
  scale: number;
  viewportSize: number;
  contentSize: number;
}

export function contentMax(layer: ScalableScrollLayer): number {
  return Math.max(0, layer.contentSize * layer.scale - layer.viewportSize);
}

export function syncBounds(layer: ScalableScrollLayer): void {
  layer.min = 0;
  layer.max = contentMax(layer);
}

export function applyScale(
  layer: ScalableScrollLayer,
  nextScale: number,
  anchorOffset: number,
): void {
  const anchorContent = (layer.position + anchorOffset) / layer.scale;
  layer.scale = nextScale;
  syncBounds(layer);
  layer.position = clamp(
    anchorContent * nextScale - anchorOffset,
    layer.min,
    layer.max,
  );
}

export function applyScaleThenScroll(
  layers: ScalableScrollLayer[],
  nextScale: number,
  anchorOffset: number,
  delta: number,
): ScrollChainResult {
  for (const layer of layers) {
    applyScale(layer, nextScale, anchorOffset);
  }
  return applyScrollDelta(layers, delta);
}
