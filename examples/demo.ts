import {
  BudgetExceeded,
  evaluate,
  MemoryLimitExceeded,
  TimeoutExceeded,
  ValidationError,
} from '../src/sandbox.ts';

function show(title: string, fn: () => unknown): void {
  try {
    const result = fn();
    console.log(`✔ ${title}:`, result);
  } catch (err) {
    const e = err as Error;
    console.log(`✘ ${title}: [${e.name}] ${e.message}`);
  }
}

console.log('--- 正常规则求值 ---');
show('vip 规则', () =>
  evaluate('age >= 18 && (country == "CN" || vip)', {
    variables: { age: 20, country: 'CN', vip: false },
  }).value,
);
show('字符串规则', () =>
  evaluate('startsWith(name, "A") && len(name) > 2', {
    variables: { name: 'Alice' },
  }).value,
);

console.log('\n--- 恶意表达式拦截 ---');
show('未授权函数 system("id")', () => evaluate('system("id")'));
show('宿主对象 process.exit(1)', () => evaluate('process.exit(1)'));
show('未知标识符 constructor', () => evaluate('constructor'));

console.log('\n--- 执行限额守护 ---');
show('无限递归 eval(self)（1000 步预算）', () => {
  try {
    evaluate('eval(self)');
  } catch (err) {
    if (err instanceof BudgetExceeded) {
      console.log(
        `  强制中断：已用 ${err.stepsUsed} 步 / 预算 ${err.maxSteps} 步`,
      );
    }
    throw err;
  }
});
show(
  '内存炸弹（字符串拼接膨胀）',
  () =>
    evaluate(Array(20).fill(`"${'x'.repeat(50)}"`).join(' + '), {
      limits: { maxMemory: 500 },
    }),
  // expect MemoryLimitExceeded
);
show('超时（timeoutMs = 0）', () =>
  evaluate('1 + 1', { limits: { timeoutMs: 0 } }),
);

console.log('\n--- 宿主进程健康检查 ---');
show('沙箱崩溃后宿主仍可求值', () => evaluate('1 + 1').value);
console.log('\n错误类型导出:', {
  BudgetExceeded: BudgetExceeded.name,
  MemoryLimitExceeded: MemoryLimitExceeded.name,
  TimeoutExceeded: TimeoutExceeded.name,
  ValidationError: ValidationError.name,
});
