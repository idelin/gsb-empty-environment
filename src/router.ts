import type { RoutedCommand } from './events.ts';

/** 异构存储系统的统一写入抽象 */
export interface Sink {
  readonly name: string;
  apply(command: RoutedCommand): Promise<void> | void;
}

export interface RouteRule {
  /** 命中的命令类型 */
  match: (command: RoutedCommand) => boolean;
  /** 目标 sink 名称 */
  sink: string;
}

export interface DispatchReceipt {
  sink: string;
  command: RoutedCommand;
  ok: boolean;
  error?: unknown;
}

/**
 * 跨异构存储系统的路由分发器。
 * 按规则把分流产物（归档包 / 删除指令）投递到注册的 Sink。
 */
export class SinkRouter {
  private sinks = new Map<string, Sink>();
  private rules: RouteRule[] = [];

  registerSink(sink: Sink): this {
    this.sinks.set(sink.name, sink);
    return this;
  }

  addRule(rule: RouteRule): this {
    this.rules.push(rule);
    return this;
  }

  /** 将一条命令分发给所有命中的 Sink，返回投递回执 */
  async dispatch(command: RoutedCommand): Promise<DispatchReceipt[]> {
    const receipts: DispatchReceipt[] = [];
    for (const rule of this.rules) {
      if (!rule.match(command)) continue;
      const sink = this.sinks.get(rule.sink);
      if (!sink) {
        receipts.push({ sink: rule.sink, command, ok: false, error: new Error(`sink not registered: ${rule.sink}`) });
        continue;
      }
      try {
        await sink.apply(command);
        receipts.push({ sink: sink.name, command, ok: true });
      } catch (error) {
        receipts.push({ sink: sink.name, command, ok: false, error });
      }
    }
    return receipts;
  }
}
