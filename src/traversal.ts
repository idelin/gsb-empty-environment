import type { FocusInfo, VNodeSpec } from './types.ts';

export class InvalidSpecError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidSpecError';
  }
}

interface ActiveGroup {
  exempt: ReadonlySet<string>;
  /** > 0 while the walk is inside a subtree exempt from this group. */
  exemptDepth: number;
}

/**
 * Full-tree expansion: walks the whole tree in document (pre-)order and
 * resolves the effective focus state of every node from inherited context
 * (hidden ancestors, disabled groups and their exemptions).
 */
export function expandTree(root: VNodeSpec): FocusInfo[] {
  const infos: FocusInfo[] = [];
  const groups: ActiveGroup[] = [];
  let hiddenDepth = 0;

  const visit = (node: VNodeSpec): void => {
    const docIndex = infos.length;

    for (const group of groups) {
      if (group.exempt.has(node.id)) group.exemptDepth += 1;
    }
    if (node.hidden === true) hiddenDepth += 1;

    const effectivelyHidden = hiddenDepth > 0;
    const blockingGroups = groups.filter((group) => group.exemptDepth === 0).length;
    const effectivelyDisabled = node.disabled === true || blockingGroups > 0;

    let tabindex: number | null = null;
    if (node.tabindex !== undefined && node.tabindex !== null) {
      if (!Number.isInteger(node.tabindex)) {
        throw new InvalidSpecError(`node "${node.id}": tabindex must be an integer`);
      }
      tabindex = node.tabindex;
    } else if (node.focusable === true) {
      tabindex = 0;
    }

    const focusable = tabindex !== null && !effectivelyHidden && !effectivelyDisabled;
    infos.push({
      id: node.id,
      docIndex,
      tabindex,
      effectivelyHidden,
      effectivelyDisabled,
      focusable,
      sequentiallyFocusable: focusable && tabindex !== null && tabindex >= 0,
    });

    let pushedGroup = false;
    if (node.group === true && node.disabled === true) {
      groups.push({ exempt: new Set(node.exempt ?? []), exemptDepth: 0 });
      pushedGroup = true;
    }
    for (const child of node.children ?? []) visit(child);
    if (pushedGroup) groups.pop();

    if (node.hidden === true) hiddenDepth -= 1;
    for (const group of groups) {
      if (group.exempt.has(node.id)) group.exemptDepth -= 1;
    }
  };

  visit(root);
  return infos;
}

/**
 * Sequential (Tab) focus order: positive tabindex values first, ordered by
 * value then document order; then zero / natively focusable nodes in
 * document order. Negative tabindex nodes are excluded.
 */
export function computeTabOrder(infos: readonly FocusInfo[]): string[] {
  const sequential = infos.filter((info) => info.sequentiallyFocusable);
  const positives = sequential
    .filter((info) => (info.tabindex as number) > 0)
    .sort(
      (a, b) =>
        (a.tabindex as number) - (b.tabindex as number) || a.docIndex - b.docIndex,
    );
  const zeros = sequential.filter((info) => info.tabindex === 0);
  return [...positives, ...zeros].map((info) => info.id);
}

/** Nodes reachable only programmatically (negative tabindex), in document order. */
export function computeProgrammaticOrder(infos: readonly FocusInfo[]): string[] {
  return infos
    .filter((info) => info.focusable && !info.sequentiallyFocusable)
    .map((info) => info.id);
}
