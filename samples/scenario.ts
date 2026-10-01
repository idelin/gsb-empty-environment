/**
 * 可重放场景：被样例运行器（replay-sample.ts）与回放测试（test/replay.test.ts）共用。
 */

import type { App } from '../src/fixtures.ts';
import type { ReplayStep } from '../src/replay.ts';

export function buildScenario(app: App): ReplayStep[] {
  const { tree, navigator } = app;
  return [
    // 1. 工具栏：方向布局 + 回环
    { kind: 'key', key: 'ArrowRight' },
    { kind: 'key', key: 'ArrowRight' },
    { kind: 'key', key: 'ArrowRight' }, // 回环到 BTN-FILE
    { kind: 'expectFocus', id: 'BTN-FILE' },

    // 2. 空间导航进入侧栏并纵向遍历；随后连续两次按键使首次慢挂载（S7）结果过期
    { kind: 'key', key: 'ArrowDown' }, // -> S1
    { kind: 'key', key: 'ArrowDown' }, // -> S2
    { kind: 'key', key: 'ArrowDown' }, // -> S3
    { kind: 'key', key: 'ArrowDown' }, // -> S4
    { kind: 'key', key: 'ArrowDown' }, // -> S5
    { kind: 'key', key: 'ArrowDown' }, // -> S6
    { kind: 'expectFocus', id: 'S6' },
    { kind: 'key', key: 'ArrowDown' }, // 请求挂载 S7（慢，100ms）
    { kind: 'key', key: 'ArrowDown' }, // 再次按键：首次挂载结果过期
    { kind: 'advance', ms: 100 },      // 挂载完成：仅第二次的结果生效
    { kind: 'expectFocus', id: 'S7' },

    // 3. S8 在视口外：先确定候选，再请求滚动，完成时校验布局版本
    { kind: 'key', key: 'ArrowDown' },
    { kind: 'advance', ms: 20 },
    { kind: 'expectFocus', id: 'S8' },

    // 4. 侧栏底部继续向下（按有效几何的主轴规则进入主区/页脚）；再向下触发回环
    { kind: 'key', key: 'ArrowDown' },
    { kind: 'expectFocus', id: 'A2' },
    { kind: 'key', key: 'ArrowDown' },
    { kind: 'expectFocus', id: 'OPEN' },
    { kind: 'key', key: 'ArrowDown' }, // 下方无候选 → 回环
    { kind: 'expectFocus', id: 'BTN-EDIT' },

    // 5. 嵌套滚动：进入主区芯片横向滚动容器
    { kind: 'mutate', note: 'pointer focus C3', fn: () => navigator.focusById('C3') },
    { kind: 'key', key: 'ArrowLeft' }, // -> H2（嵌套滚动容器内）
    { kind: 'key', key: 'ArrowRight' },// -> H3
    { kind: 'key', key: 'ArrowRight' },// H4 在视口外：请求嵌套滚动
    { kind: 'advance', ms: 20 },
    { kind: 'expectFocus', id: 'H4' },

    // 6. 打开模态：背景被遮罩遮挡，焦点自动进入模态且不能越界
    { kind: 'mutate', note: 'open modal', fn: () => tree.setVisible('MODAL', true) },
    { kind: 'expectFocus', id: 'M-CONFIRM' },
    { kind: 'key', key: 'ArrowRight' },
    { kind: 'expectFocus', id: 'M-CANCEL' },
    { kind: 'key', key: 'ArrowRight' }, // 模态内回环，不越界
    { kind: 'expectFocus', id: 'M-CONFIRM' },
    { kind: 'key', key: 'ArrowDown' },  // -> M-L1
    { kind: 'key', key: 'ArrowDown' },  // 请求挂载 M-L2（50ms）
    { kind: 'advance', ms: 50 },
    { kind: 'expectFocus', id: 'M-L2' },
    { kind: 'key', key: 'ArrowDown' },  // -> M-L3
    { kind: 'key', key: 'ArrowDown' },  // 模态内回环到顶部
    { kind: 'expectFocus', id: 'M-CONFIRM' },

    // 7. 关闭模态：焦点合法后备到触发按钮 OPEN
    { kind: 'mutate', note: 'close modal', fn: () => tree.setVisible('MODAL', false) },
    { kind: 'expectFocus', id: 'OPEN' },

    // 8. 当前焦点被删除：合法后备到逻辑次序上的下一个节点
    { kind: 'mutate', note: 'pointer focus C1', fn: () => navigator.focusById('C1') },
    { kind: 'mutate', note: 'delete C1', fn: () => tree.remove('C1') },
    { kind: 'expectFocus', id: 'C2' },

    // 9. 容器折叠：焦点在折叠容器内时回退
    { kind: 'mutate', note: 'pointer focus A1', fn: () => navigator.focusById('A1') },
    { kind: 'mutate', note: 'collapse ADV', fn: () => tree.setCollapsed('ADV', true) },
    { kind: 'expectFocus', id: 'OPEN' },

    { kind: 'runAll' },
  ];
}
