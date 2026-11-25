export type {
  AnalysisOptions,
  AnalysisReport,
  AndroidManifest,
  ChainHop,
  ComponentInfo,
  ComponentSurface,
  Finding,
  ProtectionInfo,
  ProtectionLevel,
  RedirectChain,
  RedirectEdge,
  RedirectOnlyTarget,
  Severity,
} from './types.ts';
export { parseManifest, tokenizeManifest } from './parser.ts';
export {
  analyzeExportSurface,
  createPermissionResolver,
  isSignatureLevel,
  resolveEffectiveExport,
  type EffectiveExport,
  type PermissionResolver,
} from './exportSurface.ts';
export { analyzeRedirects } from './redirectAnalysis.ts';
export { analyzeManifest } from './analyzer.ts';
