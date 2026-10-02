/**
 * 运行样例:用户注册表单
 *   node examples/demo.ts   (需要 Node >= 22.6,推荐 >= 23.6)
 *
 * 场景覆盖验收边界:
 *   1. 密码 / 确认密码交叉一致性,修改密码自动重检确认密码;
 *   2. 勾选“已婚”复选框动态为“配偶姓名”新增必填规则;
 *   3. 整表一键校验,所有状态即时同步。
 */
import {
  FormValidator,
  required,
  minLength,
  email,
  equalsField,
} from '../src/index.ts';

const form = new FormValidator({
  username: {
    rules: [required('用户名必填'), minLength(3, '用户名至少 3 个字符')],
  },
  email: {
    rules: [required('邮箱必填'), email('邮箱格式不正确')],
  },
  password: {
    rules: [required('密码必填'), minLength(8, '密码至少 8 位')],
  },
  confirmPassword: {
    rules: [required('请再次输入密码')],
    dependsOn: ['password'],
    dynamicRules: () => [equalsField('password', '两次输入的密码不一致')],
  },
  married: { rules: [] },
  spouseName: {
    dependsOn: ['married'],
    dynamicRules: (values) =>
      values.married === true ? [required('已婚后配偶姓名为必填项')] : [],
  },
});

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
  for (const name of ['username', 'email', 'password', 'confirmPassword', 'married', 'spouseName']) {
    const state = form.getState(name);
    const status = !state.validated ? '未校验' : state.valid ? '✓ 合法' : `✗ ${state.errors.join(';')}`;
    console.log(`  ${name.padEnd(16)} ${status}`);
  }
  console.log(`  整表 isValid = ${form.isValid()}`);
}

form.onUpdate((event) => {
  console.log(`  [事件] 触发源=${event.source ?? '整表校验'} 重检字段=[${event.revalidated.join(', ')}]`);
});

console.log('--- 步骤 1:空表一键校验 ---');
form.validate();
show('空表校验结果');

console.log('\n--- 步骤 2:填写密码与确认密码(一致) ---');
form.setValue('password', 'secret-123');
form.setValue('confirmPassword', 'secret-123');
show('密码一致');

console.log('\n--- 步骤 3:仅修改密码,确认密码自动重新比对 ---');
form.setValue('password', 'secret-456');
show('修改密码后(确认密码自动失效)');

console.log('\n--- 步骤 4:勾选“已婚”,配偶姓名动态变为必填 ---');
form.setValue('married', true);
show('勾选已婚后');

console.log('\n--- 步骤 5:补全所有字段,整表校验通过 ---');
form.setValue('username', 'alice');
form.setValue('email', 'alice@example.com');
form.setValue('confirmPassword', 'secret-456');
form.setValue('spouseName', '张三');
form.validate();
show('最终状态');
