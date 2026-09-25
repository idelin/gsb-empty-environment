import type { FontMeasurer } from "./measurer.ts";
import {
  measureKey,
  type LayoutBox,
  type LayoutNode,
  type LayoutResult,
  type Measurement,
  type TextStyle,
} from "./types.ts";

export interface LayoutRequest {
  text: string;
  style: TextStyle;
}

/**
 * Layout feedback: after a round completes, the host may request additional
 * measurements (e.g. content that depends on the laid-out size). The engine
 * bounds these rounds, so feedback can never spin forever.
 */
export type LayoutFeedback = (tree: LayoutBox, round: number) => LayoutRequest[];

export interface LayoutSession {
  /** Published synchronously from coarse estimate metrics. */
  readonly estimate: LayoutResult;
  /** Published once precise measurements settle and rounds stabilize. */
  readonly final: Promise<LayoutResult>;
}

const DEFAULT_MAX_ROUNDS = 8;

export class LayoutEngine {
  private readonly measurer: FontMeasurer;
  private readonly maxRounds: number;
  private readonly feedback: LayoutFeedback | undefined;

  constructor(
    measurer: FontMeasurer,
    options: { maxRounds?: number; feedback?: LayoutFeedback } = {},
  ) {
    this.measurer = measurer;
    this.maxRounds = options.maxRounds ?? DEFAULT_MAX_ROUNDS;
    this.feedback = options.feedback;
  }

  /**
   * Estimate and final layouts are published separately. The estimate is
   * computed synchronously; the final resolves after the scheduler has been
   * pumped enough to settle every shared measurement task.
   */
  layout(root: LayoutNode): LayoutSession {
    const estimateTree = layoutNode(root, root.width, (text, style) =>
      this.measurer.estimate(text, style),
    );
    const estimate: LayoutResult = { channel: "estimate", round: 0, tree: estimateTree };
    return { estimate, final: this.runFinal(root) };
  }

  private async runFinal(root: LayoutNode): Promise<LayoutResult> {
    // Same-key dedupe across the whole session: layout feedback can never
    // enqueue a second task for a key that was already requested.
    const requested = new Map<string, Promise<Measurement>>();
    const requestFinal = (text: string, style: TextStyle): Promise<Measurement> | null => {
      const key = measureKey({
        text,
        style,
        fontVersion: this.measurer.registry.versionOf(style.fontFamily) ?? 0,
      });
      if (requested.has(key)) return null; // already awaited in an earlier round
      const pending = this.measurer.measure(text, style).promise;
      requested.set(key, pending);
      return pending;
    };

    let round = 0;
    let tree: LayoutBox;
    for (;;) {
      round += 1;
      const roundRequests: Array<Promise<Measurement>> = [];
      const measure = (text: string, style: TextStyle): Measurement => {
        const cached = this.measurer.peekFinal(text, style);
        if (cached) return cached;
        const pending = requestFinal(text, style);
        if (pending) roundRequests.push(pending);
        return this.measurer.estimate(text, style);
      };
      tree = layoutNode(root, root.width, measure);

      if (this.feedback) {
        for (const extra of this.feedback(tree, round)) {
          if (this.measurer.peekFinal(extra.text, extra.style)) continue;
          const pending = requestFinal(extra.text, extra.style);
          if (pending) roundRequests.push(pending);
        }
      }

      if (roundRequests.length === 0) {
        return { channel: "final", round, tree };
      }
      // Bounded rounds: even hostile feedback terminates the loop.
      if (round >= this.maxRounds) {
        return { channel: "final", round, tree };
      }
      await Promise.all(roundRequests);
    }
  }
}

/** Pure function layout over the fixture node tree — no browser involved. */
export function layoutNode(
  node: LayoutNode,
  width: number,
  measure: (text: string, style: TextStyle) => Measurement,
): LayoutBox {
  if (node.kind === "text") {
    const measurement = measure(node.text, node.style);
    const lines = wrapLines(measurement, width);
    return {
      id: node.id,
      width: Math.min(width, lines.longest),
      height: lines.count * measurement.lineHeight,
      lines: lines.count,
      children: [],
    };
  }
  const children = node.children.map((child) => layoutNode(child, node.width, measure));
  return {
    id: node.id,
    width: node.width,
    height: children.reduce((sum, child) => sum + child.height, 0),
    lines: children.reduce((sum, child) => sum + child.lines, 0),
    children,
  };
}

/** Greedy cluster wrapping against the available inline width. */
function wrapLines(
  measurement: Measurement,
  width: number,
): { count: number; longest: number } {
  let count = 1;
  let current = 0;
  let longest = 0;
  for (const cluster of measurement.clusters) {
    if (current > 0 && current + cluster.width > width) {
      longest = Math.max(longest, current);
      count += 1;
      current = cluster.width;
    } else {
      current += cluster.width;
    }
  }
  longest = Math.max(longest, current);
  return { count, longest };
}
