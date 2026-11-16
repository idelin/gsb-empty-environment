export type TouchAction = "down" | "move" | "up" | "cancel";

export interface TouchEvent {
  action: TouchAction;
  pointerId: number;
  x: number;
  y: number;
  /** 毫秒时间戳 */
  timestamp: number;
  /** 0x1 = 窗口被完全遮挡, 0x2 = 部分被遮挡 */
  flags: number;
}

export interface ControlBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export const FLAG_FULLY_OBSCURED = 0x1;
export const FLAG_PARTIALLY_OBSCURED = 0x2;
export const OBSCURED_MASK = FLAG_FULLY_OBSCURED | FLAG_PARTIALLY_OBSCURED;
export const DEFAULT_OBSCURED_COOLDOWN_MS = 500;

export type DispatchedEventType = "down" | "move" | "up" | "cancel" | "click";

export interface DispatchedEvent {
  type: DispatchedEventType;
  pointerId: number;
  x: number;
  y: number;
  timestamp: number;
}

export interface TouchGateOptions {
  /** up 距最近一次遮挡位事件的最小间隔，默认 500ms */
  obscuredCooldownMs?: number;
}

interface PointerTrack {
  downX: number;
  downY: number;
}

function isObscured(event: TouchEvent): boolean {
  return (event.flags & OBSCURED_MASK) !== 0;
}

/**
 * 敏感控件触摸门禁：嵌入控件事件通道，按手势判定是否放行事件与点击。
 *
 * - 任一 down/move 带遮挡位 -> 整个手势作废，之后的事件一律不派发；
 * - 点击需 down 与 up 均落在控件边界内、全程无遮挡位，
 *   且 up 距最近一次遮挡位事件 >= obscuredCooldownMs；
 * - cancel 清除手势状态，但不清除遮挡冷却计时；
 * - 多指按 pointerId 分别跟踪，任一指针被遮挡则整个手势作废。
 */
export class TouchGate {
  private readonly bounds: ControlBounds;
  private readonly cooldownMs: number;
  private readonly pointers = new Map<number, PointerTrack>();
  private gestureVoided = false;
  private lastObscuredAt: number | null = null;

  constructor(bounds: ControlBounds, options: TouchGateOptions = {}) {
    this.bounds = bounds;
    this.cooldownMs = options.obscuredCooldownMs ?? DEFAULT_OBSCURED_COOLDOWN_MS;
  }

  /** 处理一个触摸事件，返回应派发到控件通道的事件列表。 */
  handle(event: TouchEvent): DispatchedEvent[] {
    if (isObscured(event)) {
      this.lastObscuredAt = event.timestamp;
    }
    switch (event.action) {
      case "down":
        return this.onDown(event);
      case "move":
        return this.onMove(event);
      case "up":
        return this.onUp(event);
      case "cancel":
        return this.onCancel(event);
    }
  }

  private onDown(event: TouchEvent): DispatchedEvent[] {
    if (this.gestureVoided) {
      // 作废手势期间仍跟踪指针，确保所有手指抬起后才结束手势。
      this.pointers.set(event.pointerId, { downX: event.x, downY: event.y });
      return [];
    }
    this.pointers.set(event.pointerId, { downX: event.x, downY: event.y });
    if (isObscured(event)) {
      this.gestureVoided = true;
      return [];
    }
    return [this.dispatch("down", event)];
  }

  private onMove(event: TouchEvent): DispatchedEvent[] {
    if (this.gestureVoided || !this.pointers.has(event.pointerId)) {
      return [];
    }
    if (isObscured(event)) {
      this.gestureVoided = true;
      return [];
    }
    return [this.dispatch("move", event)];
  }

  private onUp(event: TouchEvent): DispatchedEvent[] {
    const track = this.pointers.get(event.pointerId);
    this.pointers.delete(event.pointerId);
    if (this.gestureVoided) {
      this.endGestureIfIdle();
      return [];
    }
    if (!track) {
      return [];
    }
    const out = [this.dispatch("up", event)];
    if (
      this.inBounds(track.downX, track.downY) &&
      this.inBounds(event.x, event.y) &&
      this.cooldownSatisfied(event.timestamp)
    ) {
      out.push(this.dispatch("click", event));
    }
    this.endGestureIfIdle();
    return out;
  }

  private onCancel(event: TouchEvent): DispatchedEvent[] {
    const wasVoided = this.gestureVoided;
    // cancel 清除手势状态，但保留 lastObscuredAt 冷却计时。
    this.pointers.clear();
    this.gestureVoided = false;
    if (wasVoided) {
      return [];
    }
    return [this.dispatch("cancel", event)];
  }

  private endGestureIfIdle(): void {
    if (this.pointers.size === 0) {
      this.gestureVoided = false;
    }
  }

  private inBounds(x: number, y: number): boolean {
    const { left, top, right, bottom } = this.bounds;
    return x >= left && x <= right && y >= top && y <= bottom;
  }

  private cooldownSatisfied(timestamp: number): boolean {
    return (
      this.lastObscuredAt === null ||
      timestamp - this.lastObscuredAt >= this.cooldownMs
    );
  }

  private dispatch(type: DispatchedEventType, event: TouchEvent): DispatchedEvent {
    return {
      type,
      pointerId: event.pointerId,
      x: event.x,
      y: event.y,
      timestamp: event.timestamp,
    };
  }
}
