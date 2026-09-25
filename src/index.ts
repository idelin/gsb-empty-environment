export { createGlyphMetrics, renderText, normalizeSpace } from './metrics.ts';
export type { GlyphMetrics } from './metrics.ts';
export { NodeTree } from './tree.ts';
export type { NodeSpec, TreeNode, DrainedChanges } from './tree.ts';
export {
  computeAccessibleName,
  computeAccessibleDescription,
  computeNameAndDescription,
  PROHIBITED_NAMING_ROLES,
  NAME_FROM_CONTENT_ROLES,
  LABELABLE_ROLES,
} from './accname.ts';
export type { NameAndDescription, DepSink } from './accname.ts';
export { NameEngine } from './engine.ts';
export type { NameResult, ChangeNotification } from './engine.ts';
