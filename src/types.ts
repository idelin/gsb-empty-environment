/**
 * Node tree given by fixtures (boxes / glue / penalties), TeX-style.
 * All widths are integer fixture units; no browser layout engine involved.
 */
export type BoxNode = { kind: 'box'; width: number; text: string };
export type GlueNode = {
  kind: 'glue';
  width: number;
  stretch: number;
  shrink: number;
};
export type PenaltyNode = {
  kind: 'penalty';
  penalty: number; // cost paid when the line breaks here
  width: number; // width added to the line when broken here (e.g. hyphen)
  flagged: boolean; // true => breaking here counts as hyphenation
  text: string; // what is shown at line end when broken here, e.g. "-"
};
export type Node = BoxNode | GlueNode | PenaltyNode;

export interface BreakOptions {
  lineWidth: number;
  /** weight per unit of |naturalWidth(line i) - naturalWidth(line i-1)| */
  widthDiffWeight: number;
  /** maximum number of hyphenated lines across the whole paragraph */
  maxHyphens: number;
  /** forbid two hyphenated lines in a row (default true) */
  forbidConsecutiveHyphens: boolean;
  /** max DP relaxations before reporting budget exhaustion */
  budget: number;
}

export const OVERFLOW_PENALTY = 100000;
export const OVERFLOW_PER_UNIT = 10;
export const NO_STRETCH_BADNESS = 10000;
