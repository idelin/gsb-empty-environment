# 动态响应式表单验证与依赖交叉校验器

企业级表单响应式规则计算器(TypeScript 实现,零依赖)。字段间可声明复杂的相互依赖
(如“若勾选已婚,则配偶姓名必填”),随表单输入动态刷新各字段合法性状态与错误消息,
支持整表一键校验。

## 环境要求

- Node.js **>= 22.6**(推荐 >= 23.6,利用内置 TypeScript 类型擦除直接运行 `.ts`,无需编译)
- 无需安装任何依赖

```bash
npm test    # 运行 Node.js 内置测试(node:test)
npm run demo  # 运行注册表单样例
```

## 核心概念

| 概念 | 说明 |
| --- | --- |
| `rules` | 静态规则,始终生效 |
| `dynamicRules(values)` | 动态规则,每次校验时按整表当前值实时计算(条件必填) |
| `dependsOn` | 声明跨字段依赖;被依赖字段值变化时,本字段自动重新校验 |
| `setValue()` | 写入值并立即重检该字段及其全部依赖方 |
| `validate()` | 整表一键校验,返回整表合法性 |
| `onUpdate()` | 订阅状态变化事件(含触发源与受影响字段列表) |

## 使用示例

```ts
import { FormValidator, required, minLength, equalsField } from './src/index.ts';

const form = new FormValidator({
  password: {
    rules: [required('密码必填'), minLength(8, '密码至少 8 位')],
  },
  confirmPassword: {
    rules: [required('请再次输入密码')],
    dependsOn: ['password'],                       // 密码变化时自动重检
    dynamicRules: () => [equalsField('password', '两次输入的密码不一致')],
  },
  married: { rules: [] },
  spouseName: {
    dependsOn: ['married'],                        // 复选框变化时自动重检
    dynamicRules: (values) =>
      values.married === true ? [required('已婚后配偶姓名为必填项')] : [],
  },
});

form.setValue('password', 'secret-123');
form.setValue('confirmPassword', 'secret-123');   // ✓ 一致
form.setValue('password', 'secret-456');          // 确认密码自动变为 ✗ 两次输入的密码不一致
form.setValue('married', true);                   // 配偶姓名动态新增必填规则
form.validate();                                  // 整表一键校验
```

## 项目结构

```
src/rules.ts           内置规则库(required/minLength/pattern/email/equalsField/custom)
src/form-validator.ts  核心引擎(依赖图 + 响应式重校验 + 整表校验 + 事件订阅)
src/index.ts           统一导出
tests/form-validator.test.ts  Node.js 内置测试(9 个用例)
examples/demo.ts       注册表单运行样例
```

## 测试结果

Node.js v25.5.0,`node --test`:

```
✔ 基础规则:必填与最小长度
✔ 验收边界 1:修改密码自动触发确认密码重新比对
✔ 验收边界 2:勾选复选框动态新增必填规则,取消勾选即时移除
✔ 验收边界 3:整表一键校验,所有状态即时同步
✔ 依赖交叉校验:一个字段可同时被多个字段依赖
✔ 自定义规则与动态错误消息
✔ 响应式事件:值变化时通知订阅者并携带受影响字段
✔ 未校验前 isValid 为 false;dirty 标记正确
✔ 非法字段与非法依赖抛出明确错误
ℹ tests 9  ℹ pass 9  ℹ fail 0
```
