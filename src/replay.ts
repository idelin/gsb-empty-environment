import { CommandCenter } from './command-center.ts';
import type { CommandEvent } from './events.ts';

export interface ReplayResult {
  center: CommandCenter;
  /** 重放过程中被吞掉的错误（如注册时的条件解析错误）。 */
  errors: unknown[];
}

/**
 * 重放事件日志，从零重建命令库状态。
 * 只有输入类事件（context.set/unset、focus.changed、command.registered/unregistered）
 * 参与重放；listed/executed/rejected/parseError 是观察结果，不重复施加。
 * 重放处理器用空函数代替，因为可用性与排序只依赖元数据。
 */
export function replay(events: readonly CommandEvent[]): ReplayResult {
  const center = new CommandCenter();
  const errors: unknown[] = [];
  for (const event of events) {
    switch (event.type) {
      case 'context.set':
        center.setContext(event.key, event.value);
        break;
      case 'context.unset':
        center.unsetContext(event.key);
        break;
      case 'focus.changed':
        if (event.to === null) {
          center.blur();
        } else {
          center.focus(event.to);
        }
        break;
      case 'command.registered':
        try {
          center.register({
            id: event.id,
            priority: event.priority,
            when: event.when,
            handler: () => undefined,
          });
        } catch (err) {
          errors.push(err);
        }
        break;
      case 'command.unregistered':
        center.unregister(event.id);
        break;
      case 'command.listed':
      case 'command.executed':
      case 'command.executeRejected':
      case 'condition.parseError':
        break;
    }
  }
  return { center, errors };
}
