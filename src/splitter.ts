import { Transform } from 'node:stream';
import type {
  ChangeEvent,
  DispatchReceipt,
  Middleware,
  RoutedAction,
  Sink,
} from './types.ts';

/**
 * 流式分流器：CDC 事件 -> 中间件链 -> 路由动作 -> 异构 Sink 分发。
 * 单个 Sink 失败不影响其他 Sink（错误隔离），分发结果以回执形式向下游流动。
 */
export class Splitter {
  private readonly sinks = new Map<string, Sink>();
  private readonly middlewares: Middleware[] = [];

  registerSink(sink: Sink): this {
    this.sinks.set(sink.name, sink);
    return this;
  }

  use(middleware: Middleware): this {
    this.middlewares.push(middleware);
    return this;
  }

  async process(event: ChangeEvent): Promise<DispatchReceipt[]> {
    const actions: RoutedAction[] = [];
    const emit = (action: RoutedAction) => actions.push(action);
    for (const middleware of this.middlewares) {
      await middleware.handle(event, emit);
    }
    const receipts: DispatchReceipt[] = [];
    for (const action of actions) {
      receipts.push(await this.dispatch(action));
    }
    return receipts;
  }

  private async dispatch(action: RoutedAction): Promise<DispatchReceipt> {
    const sink = this.sinks.get(action.sink);
    if (!sink) {
      return { action, status: 'failed', error: `unknown sink: ${action.sink}` };
    }
    try {
      if (action.type === 'insert') {
        await sink.insert(action.table, action.record);
      } else {
        await sink.delete(action.table, action.id);
      }
      return { action, status: 'ok' };
    } catch (err) {
      return { action, status: 'failed', error: (err as Error).message };
    }
  }

  /** object-mode Transform：ChangeEvent 流入，DispatchReceipt 流出，可接入任意流式 CDC 管道 */
  stream(): Transform {
    const splitter = this;
    return new Transform({
      objectMode: true,
      async transform(event: ChangeEvent, _encoding, callback) {
        try {
          const receipts = await splitter.process(event);
          for (const receipt of receipts) this.push(receipt);
          callback();
        } catch (err) {
          callback(err as Error);
        }
      },
    });
  }
}
