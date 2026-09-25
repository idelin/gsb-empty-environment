export interface TreeNode {
  id: string;
  role?: string;
  attrs: Record<string, string>;
  text?: string;
  hidden?: boolean;
  children: TreeNode[];
}

export interface GlyphMetrics {
  charWidth: number;
  overrides?: Record<string, number>;
}

export type DepKey = string;

export interface NameResult {
  name: string;
  description: string;
}
