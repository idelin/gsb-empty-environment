# GraalVM宿主类查找放行规则

GraalVM 多语言上下文里，来宾脚本经 Java.type 按名字查宿主类，攻击者用内部类的 $ 名、数组类名或同前缀的另一个包绕过放行规则，取到 Runtime 与 ProcessBuilder。为 TypeScript 配置服务写供上下文创建前调用的权限校验包：输入上下文选项与宿主类放行清单，allowAllAccess 或 allowNativeAccess 开启、宿主访问策略选了全部、允许创建线程且未设资源上限一律拒绝；类名按 Java 二进制名匹配，放行项 com.acme.api. 只匹配该包及子包，com.acme.api2 与 com.acme.apix 不匹配，含 $ 的内部类继承外部类的放行，数组类 [Lcom.acme.api.Foo; 看元素类型；java.lang.reflect、java.lang.invoke 与 sun. 开头的包永不放行。用同前缀包、内部类、数组类、全放开选项和合规配置验证。

## 结构

- `src/names.ts`：Java 二进制名解析与放行匹配（内部类 `$`、数组 `[L...;`、包边界匹配、禁止包）。
- `src/context.ts`：上下文创建前的选项校验与查询闸门。
- `index.ts`：包公开入口。
- `test/`：基于 `node --test` 的 41 个用例，`npm test` 运行。

## 匹配语义

- 放行项以 `.` 结尾表示包前缀，按包边界匹配：`com.acme.api.` 命中 `com.acme.api.Foo` 与 `com.acme.api.spi.Bar`，但不命中 `com.acme.api2.Evil`、`com.acme.apix.Evil`。
- 不以 `.` 结尾的条目按精确类名匹配。
- 内部类（`Outer$Inner`、`Outer$1`）先截断到外部类再匹配，外部类未放行则内部类无法借 `$` 名绕过。
- 数组描述符（`[Lcom.acme.Foo;`、`[[L...;`）剥离维度后只看元素类型；基本类型数组（`[I`）不映射宿主类，直接拒绝。
- `java.lang.reflect.`、`java.lang.invoke.`、`sun.` 开头的类永不放行，即使出现在清单中（该清单条目本身也会被判为非法）。
- 默认拒绝：清单为空或未命中时任何宿主类都拿不到，`java.lang.Runtime`、`java.lang.ProcessBuilder` 因此默认拒绝。

## 用法

```ts
import {
  assertContextAllowed,
  checkHostClassQuery,
  validateContextPermissions,
} from './index.ts';

const allowList = ['com.acme.api.', 'com.acme.util.MathHelper'];

// 创建前 fail-fast：违规时抛 PermissionDeniedError。
assertContextAllowed(
  {
    allowAllAccess: false,
    allowNativeAccess: false,
    hostAccess: 'explicit',
    allowCreateThread: true,
    resourceLimits: { statementLimit: 5000 },
  },
  allowList,
);

// 不抛异常的版本，返回 { allowed, reasons, allowList }。
const decision = validateContextPermissions(options, allowList);

// 包装 Java.type 闸门时按原始类名裁决。
checkHostClassQuery('com.acme.api.Outer$Inner', allowList); // allowed
checkHostClassQuery('java.lang.Runtime', allowList);        // denied
```

## 硬性拒绝条件

- `allowAllAccess === true`
- `allowNativeAccess === true`
- `hostAccess === 'all'`（HostAccess.ALL）
- `allowCreateThread === true` 且未配置正数 `resourceLimits.statementLimit`
- 放行清单包含非法条目或禁止包条目
