/**
 * 事件重放器：把声明式事件序列逐条应用到 FocusManager 上，
 * 并在 expect* 事件处断言。异步恢复通过手动队列 flush 精确推进。
 */
import assert from 'node:assert/strict';
import { FocusManager } from '../src/focus-manager.ts';

export type Ev =
  | { op: 'base'; id: string }
  | { op: 'node'; id: string; layer: string; parent?: string; disabled?: boolean }
  | { op: 'remove'; id: string }
  | { op: 'disable'; id: string }
  | { op: 'enable'; id: string }
  | { op: 'open'; id: string; trigger?: string }
  | { op: 'close'; id: string; defer?: boolean }
  | { op: 'flush' }
  | { op: 'expectFocus'; id: string | null }
  | { op: 'expectStack'; ids: string[] }
  | { op: 'expectLog'; ids: (string | null)[] };

export interface ReplayHooks {
  onFocus?: (id: string | null, fm: FocusManager) => void;
}

export function replay(events: Ev[], hooks: ReplayHooks = {}): FocusManager {
  const pending: (() => void)[] = [];
  const fm = new FocusManager((fn) => pending.push(fn));
  if (hooks.onFocus) fm.onFocus((id) => hooks.onFocus!(id, fm));

  for (const ev of events) {
    switch (ev.op) {
      case 'base':
        fm.createBaseLayer(ev.id);
        break;
      case 'node':
        fm.registerNode(ev.id, ev.layer, ev.parent ?? null, ev.disabled ?? false);
        break;
      case 'remove':
        fm.removeNode(ev.id);
        break;
      case 'disable':
        fm.setDisabled(ev.id, true);
        break;
      case 'enable':
        fm.setDisabled(ev.id, false);
        break;
      case 'open':
        fm.openLayer(ev.id, ev.trigger ?? null);
        break;
      case 'close':
        fm.closeLayer(ev.id, { defer: ev.defer ?? false });
        break;
      case 'flush':
        while (pending.length > 0) pending.shift()!();
        break;
      case 'expectFocus':
        assert.strictEqual(fm.focusedId, ev.id, `expectFocus ${ev.id}`);
        break;
      case 'expectStack':
        assert.deepStrictEqual(fm.stackIds, ev.ids, `expectStack ${ev.ids}`);
        break;
      case 'expectLog':
        assert.deepStrictEqual(fm.focusLog, ev.ids, `expectLog`);
        break;
    }
  }
  return fm;
}
