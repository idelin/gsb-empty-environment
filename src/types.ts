/**
 * Specification of a virtual node in the focus tree.
 *
 * A node is "focus-eligible by nature" when it is natively focusable
 * (`focusable: true`, like a <button>) or carries an explicit `tabindex`.
 */
export interface VNodeSpec {
  /** Unique id within the whole tree. Duplicates are rejected. */
  id: string;
  /**
   * Explicit tabindex. Positive values sort before zero values; zero and
   * natively focusable nodes follow document order; negative values are
   * reachable only programmatically.
   */
  tabindex?: number;
  /** Hides this node and its entire subtree from focus traversal. */
  hidden?: boolean;
  /** Disables this node; on a `group` node it disables the whole subtree. */
  disabled?: boolean;
  /** Natively focusable element (implicit tabindex 0). */
  focusable?: boolean;
  /** Container whose `disabled` state propagates to descendants. */
  group?: boolean;
  /** Ids of descendant subtrees exempt from this group's disabled state. */
  exempt?: string[];
  children?: VNodeSpec[];
}

/** Attributes accepted by {@link FocusTree.update}. */
export interface UpdateAttrs {
  id?: string;
  tabindex?: number | null;
  hidden?: boolean;
  disabled?: boolean;
  focusable?: boolean;
  group?: boolean;
  exempt?: string[];
}

/** Per-node result of a full-tree expansion. */
export interface FocusInfo {
  id: string;
  /** Pre-order position in the tree (document order). */
  docIndex: number;
  /** Resolved tabindex: explicit value, 0 for natively focusable, else null. */
  tabindex: number | null;
  /** Hidden by itself or by an ancestor. */
  effectivelyHidden: boolean;
  /** Disabled by itself or by a non-exempt disabled ancestor group. */
  effectivelyDisabled: boolean;
  /** Reachable by any means (sequential or programmatic). */
  focusable: boolean;
  /** Reachable via sequential (Tab) navigation. */
  sequentiallyFocusable: boolean;
}
