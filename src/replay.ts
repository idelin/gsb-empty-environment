import type { Rect } from './geometry.ts';
import type { FocusController, KeyResult } from './controller.ts';
import type { LayoutTree, NodeId } from './tree.ts';

/** Synthetic keyboard event — mirrors the shape of a DOM KeyboardEvent. */
export interface KeyEventLike {
  type: 'keydown';
  key: string;
}

export function keyEvent(key: string): KeyEventLike {
  return { type: 'keydown', key };
}

export type ReplayStep =
  | { type: 'press'; event: KeyEventLike } // fire-and-forget key press (models rapid typing)
  | { type: 'key'; event: KeyEventLike } // key press awaited to completion
  | { type: 'delete'; id: NodeId }
  | { type: 'collapse'; id: NodeId; collapsed?: boolean }
  | { type: 'setRect'; id: NodeId; rect: Rect }
  | { type: 'mount'; id: NodeId }
  | { type: 'completeMount'; id: NodeId }
  | { type: 'completeScroll'; id: NodeId }
  | { type: 'settle' }; // drain all pending mounts/scrolls and in-flight keys

export interface StepRecord {
  step: ReplayStep;
  focus: NodeId | null;
  /** Whether any focusable-visible node existed when this step completed. */
  anyFocusable: boolean;
  /** Whether the focus at record time was a valid, focusable node. */
  focusValid: boolean;
  result?: KeyResult;
}

export interface ReplayResult {
  records: StepRecord[];
  finalFocus: NodeId | null;
}

/**
 * Deterministic keyboard-event replay. Async mount/scroll work only
 * completes when the script says so ('settle' / 'completeMount' /
 * 'completeScroll'), which makes slow-mount races reproducible.
 */
export async function replay(
  tree: LayoutTree,
  controller: FocusController,
  steps: ReplayStep[],
): Promise<ReplayResult> {
  const records: StepRecord[] = [];
  for (const step of steps) {
    let result: KeyResult | undefined;
    switch (step.type) {
      case 'press':
        void controller.handleKeyEvent(step.event);
        break;
      case 'key':
        result = await controller.handleKeyEvent(step.event);
        break;
      case 'delete':
        tree.delete(step.id);
        break;
      case 'collapse':
        tree.setCollapsed(step.id, step.collapsed ?? true);
        break;
      case 'setRect':
        tree.setRect(step.id, step.rect);
        break;
      case 'mount':
        tree.mountNow(step.id);
        break;
      case 'completeMount':
        tree.completeMount(step.id);
        break;
      case 'completeScroll':
        tree.completeScroll(step.id);
        break;
      case 'settle': {
        for (let round = 0; round < 16; round += 1) {
          await tree.completeAllPending();
          await controller.awaitIdle();
          controller.ensureFocus();
          if (!tree.hasPending() && !controller.busy()) break;
          if (round === 15) throw new Error('replay settle did not converge');
        }
        break;
      }
    }
    records.push({
      step,
      focus: controller.focusId,
      anyFocusable: tree.logicalOrder().some((id) => tree.isFocusableNow(id)),
      focusValid: controller.focusId !== null && tree.isFocusableNow(controller.focusId),
      result,
    });
  }
  return { records, finalFocus: controller.focusId };
}
