export * from './types.ts';
export { segmentGraphemes } from './grapheme.ts';
export { MetricsStore } from './metrics.ts';
export { flatten } from './flatten.ts';
export { layoutAtoms, type LayoutParams, type EllipsisMode } from './layout.ts';
export { costOf, scoreOf, exhaustiveKeepSet, greedyKeepSet, type KeepSetInput } from './optimal.ts';
export { caretFromPoint, selectionRects, copyText, linkFragments } from './mapping.ts';
export { TruncationEngine, type EngineState } from './engine.ts';
