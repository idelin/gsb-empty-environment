# 动态响应式表单验证与依赖交叉校验器

企业级表单响应式规则计算器（TypeScript，零运行时依赖）。字段间可声明复杂依赖
（如「若勾选已婚，则配偶姓名必填」「确认密码必须与密码一致」），输入变化时引擎
依据依赖图即时刷新相关字段的合法性与错误消息，并支持整表一键校验。

## 运行环境

- Node.js >= 22.6（使用内置类型擦除直接运行 `.ts`，无需编译步骤）
- 测试使用 Node 内置 `node:test`，无需安装任何依赖

## 快速开始

```bash
npm test    # 运行全部内置测试（node:test）
npm run demo  # 运行交互式场景样例
```

如本地装有 TypeScript，可执行 `npm run typecheck` 做严格类型检查（非必需）。

## 核心概念

- **Rule**（`src/rules.ts`）：返回错误消息或 `null`；通过 `dependsOn` 声明读取的
  其他字段，通过 `when` 声明动态生效条件。
- **FormValidator**（`src/form-validator.ts`）：根据所有规则的 `dependsOn` 构建
  依赖图；`setValue()` 会重校验被改字段及所有（传递）依赖它的字段；
  `validateAll()` 一键整表校验；`subscribe()` 订阅字段/表单级事件。

## 内置规则

`required` / `requiredWhen` / `minLength` / `maxLength` / `pattern` / `email` /
`matchesField` / `custom`

## 示例

```ts
import { FormValidator } from "./src/form-validator.ts";
import { matchesField, minLength, required, requiredWhen } from "./src/rules.ts";

const form = new FormValidator({
  password: { rules: [required(), minLength(8)] },
  confirmPassword: {
    rules: [required(), matchesField("password", "两次输入的密码不一致")],
  },
  married: { initial: false },
  spouseName: {
    rules: [requiredWhen((v) => v.married === true, ["married"], "配偶姓名必填")],
  },
});

form.setValue("password", "abcdefgh");
form.setValue("confirmPassword", "abcdefgh");
form.setValue("password", "abcdefgX"); // confirmPassword 自动重新比对 -> 报错

form.setValue("married", true); // spouseName 动态变为必填
form.validateAll(); // 整表一键校验
```

完整可运行样例见 `examples/demo.ts`，示例表单见 `examples/registration-form.ts`。

## 项目结构

```
src/rules.ts            规则定义与内置规则工厂
src/form-validator.ts   响应式校验引擎（依赖图 + 事件）
src/index.ts            公共导出入口
examples/               注册表单 schema 与场景演示
tests/                  node:test 测试套件
```
