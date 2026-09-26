/**
 * 布局引擎：
 * - 遍历节点树，对每个文本节点发起测量；
 * - 最终测量到达后可通过 feedback 生成新树（布局反馈）；
 * - 反馈产生的新测量请求受 maxPasses 约束，绝不形成无界循环。
 */
import { TextMeasurer } from './measure.ts';
import type { LayoutNode, MeasureResult, TextNode } from './types.ts';

export interface LayoutPass {
  readonly pass: number;
  readonly measured: ReadonlyMap<string, MeasureResult>;
}

/**
 * 反馈函数：根据本轮测量结果返回下一轮要布局的节点树；
 * 返回 null 表示已收敛。
 */
export type LayoutFeedback = (pass: LayoutPass) => LayoutNode | null;

export interface LayoutResult {
  readonly passes: number;
  readonly converged: boolean;
  readonly measured: ReadonlyMap<string, MeasureResult>;
  /** 全程产生的测量键，用于验证请求有界。 */
  readonly requestedKeys: readonly string[];
}

export class LayoutEngine {
  private readonly measurer: TextMeasurer;
  private readonly maxPasses: number;

  constructor(measurer: TextMeasurer, maxPasses = 8) {
    this.measurer = measurer;
    this.maxPasses = maxPasses;
  }

  layout(root: LayoutNode, feedback?: LayoutFeedback): LayoutResult {
    const requestedKeys: string[] = [];
    let current = root;
    let pass = 0;

    for (;;) {
      const measured = new Map<string, MeasureResult>();
      for (const node of collectTextNodes(current)) {
        this.measurer.measureText(node.text, node.style, (event) => {
          if (event.phase === 'final') measured.set(event.key, event.result);
          if (!requestedKeys.includes(event.key)) requestedKeys.push(event.key);
        });
      }
      // 驱动调度器直到本轮静默（依赖链逐层就绪）。
      for (;;) {
        const started = this.measurer.scheduler.pump();
        const delivered = this.measurer.scheduler.flush();
        if (started === 0 && delivered === 0) break;
      }
      pass++;

      if (!feedback) return { passes: pass, converged: true, measured, requestedKeys };
      const next = feedback({ pass, measured });
      if (next === null) return { passes: pass, converged: true, measured, requestedKeys };
      if (pass >= this.maxPasses) {
        // 反馈未收敛：强制停止，请求数量随 maxPasses 有界。
        return { passes: pass, converged: false, measured, requestedKeys };
      }
      current = next;
    }
  }
}

export function collectTextNodes(node: LayoutNode): TextNode[] {
  if (node.kind === 'text') return [node];
  const out: TextNode[] = [];
  for (const child of node.children) out.push(...collectTextNodes(child));
  return out;
}
