/** 生命周期日志：每个事件都带一条可解释的中文 message，便于直接阅读或渲染。 */

export type LifecycleEventType =
  | "defined"
  | "load-start"
  | "load-success"
  | "attach"
  | "dependency-registered"
  | "release"
  | "release-ignored"
  | "unload-start"
  | "dispose"
  | "dispose-error"
  | "dependency-release"
  | "unloaded"
  | "load-failure"
  | "rollback"
  | "cycle-detected";

export interface LifecycleEvent {
  readonly type: LifecycleEventType;
  /** 事件所属的模块名。 */
  readonly module: string;
  /** 模块实例的代数（同名模块每重新装载一次递增）。 */
  readonly generation?: number;
  /** 可解释的事件说明。 */
  readonly message: string;
  readonly timestamp: number;
  readonly detail?: Record<string, unknown>;
}

export interface LifecycleLogger {
  log(event: LifecycleEvent): void;
}

/** 默认日志器：把可解释的消息输出到控制台。 */
export class ConsoleLogger implements LifecycleLogger {
  log(event: LifecycleEvent): void {
    console.log(`[lifecycle:${event.type}] ${event.message}`);
  }
}

/** 静默日志器：显式关闭日志时使用。 */
export class SilentLogger implements LifecycleLogger {
  log(): void {}
}

/** 内存日志器：收集事件，可渲染为带序号的中文日志文本，便于测试与排障。 */
export class MemoryLogger implements LifecycleLogger {
  readonly events: LifecycleEvent[] = [];

  log(event: LifecycleEvent): void {
    this.events.push(event);
  }

  ofType(type: LifecycleEventType): LifecycleEvent[] {
    return this.events.filter((event) => event.type === type);
  }

  render(): string {
    return this.events
      .map((event, index) => `${String(index + 1).padStart(3, " ")}. [${event.type}] ${event.message}`)
      .join("\n");
  }
}
