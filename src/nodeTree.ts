/**
 * Status node tree supplied by fixtures (stands in for a DOM/accessibility
 * tree). Text extraction is a pure tree walk — no browser layout engine.
 */
export interface StatusNode {
  role?: string;
  hidden?: boolean;
  text?: string;
  children?: StatusNode[];
}

/**
 * Collect the announceable text of the subtree rooted at `node`:
 * hidden subtrees are skipped, text fragments are joined in document order.
 */
export function collectStatusText(node: StatusNode): string {
  const parts: string[] = [];
  const visit = (n: StatusNode): void => {
    if (n.hidden) return;
    if (typeof n.text === "string" && n.text.length > 0) parts.push(n.text);
    for (const child of n.children ?? []) visit(child);
  };
  visit(node);
  return parts.join(" ").replace(/\s+/g, " ").trim();
}
