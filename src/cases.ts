// ---------------------------------------------------------------------------
// 测试用例：覆盖零除、重复子表达式、条件已知但分支含错误、短路求值、
// 求值顺序与首个错误等关键场景。
// ---------------------------------------------------------------------------

export const CASES: Array<{ name: string; src: string }> = [
  // 纯常量折叠
  { name: 'const-arith',        src: '1 + 2 * 3' },
  { name: 'const-div-ok',       src: '10 / 2 - 5' },
  { name: 'bool-coercion',      src: 'true + 1 * (2 - 1)' },

  // 零除：常量除零在不同位置
  { name: 'div-zero-pure',      src: '1 / 0' },
  { name: 'div-zero-zero',      src: '0 / 0' },
  { name: 'div-zero-after-var', src: 'x + 1 / 0' },   // 先读 x 再报错
  { name: 'div-zero-before-var',src: '1 / 0 + x' },   // 报错，x 不得被读取
  { name: 'div-zero-dynamic',   src: 'x / (y - y)' }, // 运行期才知的除零
  { name: 'div-zero-first',     src: '1 / 0 + 2 / 0' },// 首个错误必须是左边的除法

  // 算术无短路：0 * e 仍要求值 e
  { name: 'no-arith-shortcut',  src: '0 * (1 / 0)' },
  { name: 'zero-times-var',     src: '0 * x' },       // 不能化简掉 x 的读取

  // 短路逻辑：未执行分支不得提前报错
  { name: 'and-false-dead-err', src: 'false && 1 / 0' },
  { name: 'or-true-dead-err',   src: 'true || 1 / 0' },
  { name: 'and-true-live-err',  src: 'true && 1 / 0' },
  { name: 'or-false-live-err',  src: 'false || 1 / 0' },
  { name: 'and-var-live-err',   src: 'x && 1 / 0' },  // x 为真才报错
  { name: 'or-var-dead-err',    src: 'x || 1 / 0' },  // x 为真则跳过错误
  { name: 'and-const-fold',     src: '2 && 3' },
  { name: 'or-const-fold',      src: '0 || 7' },
  { name: 'and-var-const',      src: 'x && true' },
  { name: 'logic-nested',       src: '(1 - 1) && x || y' },

  // 条件已知但分支含错误
  { name: 'cond-true-dead-err', src: 'true ? 1 : 1 / 0' },
  { name: 'cond-false-dead-err',src: 'false ? 1 / 0 : 2' },
  { name: 'cond-true-live-err', src: 'true ? 1 / 0 : 2' },
  { name: 'cond-var-branch-err',src: 'x ? 1 / 0 : y' },
  { name: 'cond-const-fold',    src: '2 - 2 ? 1 / 0 : x + 1' },
  { name: 'cond-nested',        src: 'x ? (y ? 1 : 1 / 0) : 0' },

  // 重复子表达式：每次出现独立求值、独立 ID
  { name: 'repeat-subexpr',     src: '(a + 1) * (a + 1)' },
  { name: 'repeat-var',         src: 'x * x + x' },
  { name: 'repeat-err',         src: '(1 / 0) * (1 / 0)' },
  { name: 'repeat-mixed',       src: '(x + 2) && (x + 2)' },

  // 混合：顺序与首个错误
  { name: 'order-reads',        src: 'x * 1 + y * 2' },
  { name: 'order-err-vs-read',  src: '(z - z) * (1 / 0) + w' },
  { name: 'order-shortcircuit', src: 'x && y || z' },
];
