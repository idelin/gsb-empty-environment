/**
 * 轨迹游标：以显式栈惰性展开循环压缩段。
 *
 * 无论 repeat 多大（例如 1e9），都只在 next() 被调用时
 * 推进一层，绝不一次性展开整个轨迹；clone() 只复制栈帧，
 * 用于有界前瞻。
 */
import { LoopSegment, StepEvent, TraceEvent } from './types';

interface Frame {
  events: TraceEvent[];
  index: number;
  /** 当前处于第几轮（从 1 计），仅对循环体帧有意义 */
  iteration: number;
  /** 该层需重复的总轮数；普通序列帧为 1 */
  repeat: number;
  /** 路径片段，例如 "trace" 或 "trace[4].loop" */
  path: string;
}

export interface CursorItem {
  event: StepEvent;
  /** 惰性展开视角下的步骤序号，从 0 开始 */
  stepIndex: number;
  path: string;
}

export class TraceCursor {
  private stack: Frame[];
  private stepIndex = 0;

  constructor(events: TraceEvent[], rootPath = 'trace') {
    this.stack = [{ events, index: 0, iteration: 1, repeat: 1, path: rootPath }];
  }

  /** 复制当前游标状态（O(栈深)），用于前瞻而不消耗主游标 */
  clone(): TraceCursor {
    const c = Object.create(TraceCursor.prototype) as TraceCursor;
    c.stack = this.stack.map((f) => ({ ...f }));
    c.stepIndex = this.stepIndex;
    return c;
  }

  /** 取下一个步骤事件；耗尽返回 null */
  next(): CursorItem | null {
    while (this.stack.length > 0) {
      const top = this.stack[this.stack.length - 1];
      if (top.index >= top.events.length) {
        // 当前层事件走完：若还需重复则回到开头进入下一轮，否则弹栈
        if (top.iteration < top.repeat) {
          top.index = 0;
          top.iteration += 1;
          continue;
        }
        this.stack.pop();
        continue;
      }
      const event = top.events[top.index];
      top.index += 1;
      if (isLoop(event)) {
        if (event.repeat <= 0) continue;
        this.stack.push({
          events: event.body,
          index: 0,
          iteration: 1,
          repeat: event.repeat,
          path: `${top.path}[${top.index - 1}].loop`,
        });
        continue;
      }
      const item: CursorItem = {
        event,
        stepIndex: this.stepIndex,
        path: `${top.path}${iterationSuffix(top)}[${top.index - 1}]`,
      };
      this.stepIndex += 1;
      return item;
    }
    return null;
  }
}

function iterationSuffix(frame: Frame): string {
  return frame.repeat > 1 ? `(第${frame.iteration}轮)` : '';
}

function isLoop(e: TraceEvent): e is LoopSegment {
  return e.kind === 'loop';
}
