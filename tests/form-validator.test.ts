import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FormValidator,
  required,
  minLength,
  email,
  equalsField,
  custom,
} from '../src/index.ts';

/** 构造验收场景表单:注册表单(密码/确认密码/已婚->配偶姓名) */
function createRegistrationForm() {
  return new FormValidator({
    username: {
      rules: [required('用户名必填'), minLength(3, '用户名至少 3 个字符')],
    },
    email: {
      rules: [required('邮箱必填'), email()],
    },
    password: {
      rules: [required('密码必填'), minLength(8, '密码至少 8 位')],
    },
    confirmPassword: {
      rules: [required('请再次输入密码')],
      dependsOn: ['password'],
      dynamicRules: () => [equalsField('password', '两次输入的密码不一致')],
    },
    married: {
      rules: [],
    },
    spouseName: {
      dependsOn: ['married'],
      dynamicRules: (values) =>
        values.married === true ? [required('已婚后配偶姓名为必填项')] : [],
    },
  });
}

test('基础规则:必填与最小长度', () => {
  const form = new FormValidator({
    name: { rules: [required(), minLength(3)] },
  });
  form.setValue('name', '');
  assert.deepEqual(form.getErrors('name'), ['该字段为必填项']);

  form.setValue('name', 'ab');
  assert.deepEqual(form.getErrors('name'), ['长度不能少于 3 个字符']);

  form.setValue('name', 'abc');
  assert.equal(form.getState('name').valid, true);
  assert.deepEqual(form.getErrors('name'), []);
});

test('验收边界 1:修改密码自动触发确认密码重新比对', () => {
  const form = createRegistrationForm();

  form.setValue('password', 'secret-123');
  form.setValue('confirmPassword', 'secret-123');
  assert.equal(form.getState('confirmPassword').valid, true);

  // 修改密码 -> 确认密码应立即变为不合法,无需手动触碰确认密码字段
  form.setValue('password', 'secret-456');
  const state = form.getState('confirmPassword');
  assert.equal(state.invalid, true);
  assert.deepEqual(state.errors, ['两次输入的密码不一致']);

  // 确认密码跟随修正后恢复合法
  form.setValue('confirmPassword', 'secret-456');
  assert.equal(form.getState('confirmPassword').valid, true);
});

test('验收边界 2:勾选复选框动态新增必填规则,取消勾选即时移除', () => {
  const form = createRegistrationForm();

  // 未婚:配偶姓名无必填要求
  form.setValue('married', false);
  assert.equal(form.getState('spouseName').valid, true);

  // 勾选已婚 -> 动态新增必填规则,状态即时同步为不合法
  form.setValue('married', true);
  assert.deepEqual(form.getErrors('spouseName'), ['已婚后配偶姓名为必填项']);

  // 填写配偶姓名 -> 合法
  form.setValue('spouseName', '张三');
  assert.equal(form.getState('spouseName').valid, true);

  // 取消勾选 -> 必填规则移除,即使清空值也合法
  form.setValue('spouseName', '');
  form.setValue('married', false);
  assert.equal(form.getState('spouseName').valid, true);
  assert.deepEqual(form.getErrors('spouseName'), []);
});

test('验收边界 3:整表一键校验,所有状态即时同步', () => {
  const form = createRegistrationForm();

  // 空表一键校验 -> 整表不合法,各字段错误齐全
  assert.equal(form.validate(), false);
  assert.equal(form.isValid(), false);
  const errors = form.getAllErrors();
  assert.deepEqual(errors.username, ['用户名必填']);
  assert.deepEqual(errors.password, ['密码必填']);
  assert.deepEqual(errors.confirmPassword, ['请再次输入密码']);
  assert.ok(!('spouseName' in errors)); // 未婚时配偶姓名无错误

  // 填满合法值后一键校验通过
  form.setValue('username', 'alice');
  form.setValue('email', 'alice@example.com');
  form.setValue('password', 'secret-123');
  form.setValue('confirmPassword', 'secret-123');
  form.setValue('married', false);
  assert.equal(form.validate(), true);
  assert.equal(form.isValid(), true);
  assert.deepEqual(form.getAllErrors(), {});
});

test('依赖交叉校验:一个字段可同时被多个字段依赖', () => {
  const form = new FormValidator({
    password: { rules: [required()] },
    confirmA: {
      dependsOn: ['password'],
      dynamicRules: () => [equalsField('password', 'A 不一致')],
    },
    confirmB: {
      dependsOn: ['password'],
      dynamicRules: () => [equalsField('password', 'B 不一致')],
    },
  });
  form.setValue('password', 'p1');
  form.setValue('confirmA', 'p1');
  form.setValue('confirmB', 'p1');
  assert.equal(form.getState('confirmA').valid, true);
  assert.equal(form.getState('confirmB').valid, true);

  form.setValue('password', 'p2');
  assert.deepEqual(form.getErrors('confirmA'), ['A 不一致']);
  assert.deepEqual(form.getErrors('confirmB'), ['B 不一致']);
});

test('自定义规则与动态错误消息', () => {
  const form = new FormValidator({
    age: {
      rules: [
        custom(
          'adult',
          (value) =>
            typeof value === 'number' && value >= 18
              ? true
              : `年龄 ${String(value)} 未满 18 岁`,
          '默认消息',
        ),
      ],
    },
  });
  form.setValue('age', 16);
  assert.deepEqual(form.getErrors('age'), ['年龄 16 未满 18 岁']);
  form.setValue('age', 20);
  assert.equal(form.getState('age').valid, true);
});

test('响应式事件:值变化时通知订阅者并携带受影响字段', () => {
  const form = createRegistrationForm();
  const events: Array<{ source: string | null; revalidated: readonly string[] }> = [];
  const unsubscribe = form.onUpdate((event) => events.push(event));

  form.setValue('password', 'secret-123');
  assert.equal(events.length, 1);
  assert.equal(events[0].source, 'password');
  assert.deepEqual(events[0].revalidated, ['password', 'confirmPassword']);

  form.validate();
  assert.equal(events[1].source, null);
  assert.equal(events[1].revalidated.length, 6);

  unsubscribe();
  form.setValue('password', 'secret-999');
  assert.equal(events.length, 2); // 退订后不再接收
});

test('未校验前 isValid 为 false;dirty 标记正确', () => {
  const form = createRegistrationForm();
  assert.equal(form.isValid(), false);
  assert.equal(form.getState('username').dirty, false);
  form.setValue('username', 'alice');
  assert.equal(form.getState('username').dirty, true);
});

test('非法字段与非法依赖抛出明确错误', () => {
  assert.throws(
    () => new FormValidator({ a: { dependsOn: ['ghost'] } }),
    /依赖了未注册的字段 "ghost"/,
  );
  const form = new FormValidator({ a: {} });
  assert.throws(() => form.setValue('nope', 1), /未注册的字段: "nope"/);
});
