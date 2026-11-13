/**
 * 触摸遮挡门禁（Touch Gate）
 *
 * 嵌入敏感控件（如支付/授权确认按钮）的事件通道：逐事件判定遮挡位、
 * 控件边界与冷却时间，只把可信的点击派发给宿主控件。
 */

/** 触摸动作。 */
export type TouchAction = "down" | "move" | "up" | "cancel";

/** flags 位：窗口被完全遮挡。 */
export const FLAG_FULLY_OCCLUDED = 0x1;
/** flags 位：窗口被部分遮挡。 */
export const FLAG_PARTIALLY_OCCLUDED = 0x2;
/** 任一遮挡位的掩码。 */
export const OCCLUSION_MASK = FLAG_FULLY_OCCLUDED | FLAG_PARTIALLY_OCCLUDED;

/** 默认遮挡冷却时间（毫秒）。 */
export const DEFAULT_OCCLUSION_COOLDOWN_MS = 500;

/** 输入的原始触摸事件。 */
export interface TouchEvent {
  action: TouchAction;
  pointerId: number;
  x: number;
  y: number;
  /** 毫秒时间戳。 */
  timestamp: number;
  /** 位标记：0x1 完全遮挡，0x2 部分遮挡，可组合出现。 */
  flags: number;
}

/** 敏感控件边界，闭区间（边界上的点视为在控件内）。 */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** 通过门禁后派发的点击事件。 */
export interface GatedClick {
  pointerId: number;
  x: number;
  y: number;
  /** 触发点击的 up 事件时间戳。 */
  timestamp: number;
}

/** 门禁配置。 */
export interface TouchGateOptions {
  /** 敏感控件边界。 */
  bounds: Rect;
  /** 遮挡冷却毫秒数，默认 500。 */
  cooldownMs?: number;
  /** 可选的点击接收方，嵌入控件事件通道时直接接入。 */
  sink?: (click: GatedClick) => void;
}

interface PointerState {
  downInside: boolean;
}

/**
 * 有状态的触摸门禁。
 *
 * 规则：
 * - 任一 down/move/up/cancel 带遮挡位，整个手势立即作废，后续事件一律不派发；
 * - 点击派发要求该指针 down 与 up 都落在控件边界内（闭区间）；
 * - up 时刻距最近一次带遮挡位的事件至少 cooldownMs 毫秒；
 * - cancel 清除手势状态，但保留遮挡冷却计时；
 * - 多指按 pointerId 分别跟踪，任一指针触发遮挡则整个手势作废。
 */
export class TouchGate {
  private bounds: Rect;
  private readonly cooldownMs: number;
  private readonly sink?: (click: GatedClick) => void;

  /** 各活跃指针的状态，按 pointerId 区分。 */
  private readonly pointers = new Map<number, PointerState>();
  /** 当前手势是否已因遮挡作废。 */
  private gestureVoid = false;
  /** 最近一次带遮挡位事件的时间戳；null 表示从未出现遮挡。 */
  private lastOcclusionAt: number | null = null;

  constructor(options: TouchGateOptions) {
    this.bounds = options.bounds;
    this.cooldownMs = options.cooldownMs ?? DEFAULT_OCCLUSION_COOLDOWN_MS;
    this.sink = options.sink;
  }

  /** 布局变化时更新控件边界。 */
  setBounds(bounds: Rect): void {
    this.bounds = bounds;
  }

  /**
   * 清除当前手势的全部指针与作废状态。
   * 遮挡冷却计时不受影响（cancel 语义）。
   */
  reset(): void {
    this.pointers.clear();
    this.gestureVoid = false;
  }

  /** 给定时间戳是否仍处于遮挡冷却内。 */
  isCoolingDown(timestamp: number): boolean {
    return (
      this.lastOcclusionAt !== null &&
      timestamp - this.lastOcclusionAt < this.cooldownMs
    );
  }

  /**
   * 处理一个触摸事件。
   *
   * @returns 通过门禁的点击；该事件未产生点击时返回 null（被拦截或仅为轨迹事件）。
   */
  processEvent(event: TouchEvent): GatedClick | null {
    const occluded = (event.flags & OCCLUSION_MASK) !== 0;
    if (occluded) {
      this.lastOcclusionAt = event.timestamp;
      this.gestureVoid = true;
    }

    switch (event.action) {
      case "down": {
        if (this.pointers.size === 0) {
          this.gestureVoid = occluded;
        }
        this.pointers.set(event.pointerId, {
          downInside: this.inBounds(event.x, event.y),
        });
        return null;
      }

      case "move": {
        const pointer = this.pointers.get(event.pointerId);
        if (pointer && !this.inBounds(event.x, event.y)) {
          pointer.downInside = false;
        }
        return null;
      }

      case "up": {
        const pointer = this.pointers.get(event.pointerId);
        const upInside = this.inBounds(event.x, event.y);
        this.pointers.delete(event.pointerId);

        const click =
          !this.gestureVoid &&
          pointer !== undefined &&
          pointer.downInside &&
          upInside &&
          !this.isCoolingDown(event.timestamp);

        if (this.pointers.size === 0) {
          this.gestureVoid = false;
        }

        if (click) {
          const gatedClick: GatedClick = {
            pointerId: event.pointerId,
            x: event.x,
            y: event.y,
            timestamp: event.timestamp,
          };
          this.sink?.(gatedClick);
          return gatedClick;
        }
        return null;
      }

      case "cancel": {
        this.reset();
        return null;
      }

      default:
        return null;
    }
  }

  /**
   * 依次处理整个触摸事件序列，返回所有通过门禁的点击。
   * 若构造时提供了 sink，每个点击同样会投递给 sink。
   */
  processSequence(events: readonly TouchEvent[]): GatedClick[] {
    const clicks: GatedClick[] = [];
    for (const event of events) {
      const click = this.processEvent(event);
      if (click) {
        clicks.push(click);
      }
    }
    return clicks;
  }

  private inBounds(x: number, y: number): boolean {
    return (
      x >= this.bounds.x &&
      x <= this.bounds.x + this.bounds.width &&
      y >= this.bounds.y &&
      y <= this.bounds.y + this.bounds.height
    );
  }
}

/** 便捷入口：对一条事件序列做一次性门禁判定。 */
export function gateTouchSequence(
  events: readonly TouchEvent[],
  options: TouchGateOptions,
): GatedClick[] {
  return new TouchGate(options).processSequence(events);
}
