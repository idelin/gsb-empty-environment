# ServiceWorker作用域覆盖

安全测试浏览器注册工作线程时，宽作用域不能由脚本路径任意扩张。写可导入的TypeScript注册规划包，脚本URL、允许作用域头和请求作用域统一按路径段规范化，禁止跨来源与编码分隔符歧义；激活新版本与旧客户控制权分离。用本地URL模型测试重定向脚本、范围重叠与迟到安装完成。

## 包结构

纯 TypeScript、零运行时依赖，经根 `index.ts` 或子路径导出导入：

- `src/url.ts`：本地 URL 模型，统一按路径段规范化并做来源/分隔符校验。
- `src/scope.ts`：单次注册决策，计算默认作用域、`Service-Worker-Allowed` 最大作用域与最终作用域。
- `src/planner.ts`：多注册规划器，检测作用域重叠并选择最具体匹配。
- `src/lifecycle.ts`：版本安装/等待/激活状态机与客户控制权模型。
- `src/errors.ts`：`RegistrationSecurityError` 与稳定错误码。

## 安全模型

- **路径段规范化**：脚本 URL、允许作用域头、请求作用域都经同一 `normalizeUrl` 解析为段数组；`.`/`..` 由 URL 解析折叠，查询串与片段不参与决策，作用域以斜杠结尾的目录段表示，匹配只按整段前缀进行（`/app` 不会覆盖 `/application`）。
- **禁止跨来源**：脚本、重定向链每一跳、SWA 头、请求作用域必须与客户页面同源（协议+主机+端口一致），任何跨源跳转或跨源头立即拒绝。
- **禁止编码分隔符歧义**：原始反斜杠、`%2f`、`%5c`（大小写均可）及非法百分号转义在规范化阶段拒绝，防止不同解析器对段边界产生分歧。
- **宽作用域约束**：默认最大作用域是最终脚本 URL 的所在目录；只有 `Service-Worker-Allowed` 头能扩张，且头值必须是脚本目录的祖先目录，请求作用域不得超出头声明的最大作用域。
- **重定向脚本**：同源重定向以链上最终 URL 作为有效脚本 URL 计算默认作用域，重定向后的扩张仍受头约束。
- **控制权分离**：新版本安装完成后进入 `waiting`，不抢占仍被旧版本控制的客户；旧客户全部卸载后才可常规激活，`force` 仅切换注册的 active 版本而不静默改写存量客户，需导航或显式 `claim` 才接管；迟到的安装完成若版本已被取代为 `redundant` 会被拒绝。

## 用法

```ts
import { planRegistration, RegistrationPlanner, RegistrationModel } from './index.ts';

const plan = planRegistration({
  clientUrl: 'https://app.example.com/app/page',
  scriptUrl: 'https://app.example.com/app/js/sw.js',
  redirectChain: ['https://app.example.com/app/v2/sw.js'],
  serviceWorkerAllowed: 'https://app.example.com/app/',
  requestedScope: 'https://app.example.com/app/',
});

const model = new RegistrationModel();
const handle = model.startInstall({
  origin: plan.origin,
  scope: plan.scope,
  scriptUrl: plan.effectiveScriptUrl,
});
handle.complete();
```

校验失败时抛出 `RegistrationSecurityError`，其 `code` 可取：`CROSS_ORIGIN_SCRIPT`、`CROSS_ORIGIN_REDIRECT`、`CROSS_ORIGIN_SCOPE`、`ENCODED_SEPARATOR_AMBIGUITY`、`INVALID_PERCENT_ENCODING`、`HEADER_SCOPE_NOT_ANCESTOR`、`SCOPE_OUTSIDE_DEFAULT`、`SCOPE_OUTSIDE_ALLOWED_HEADER` 等。

## 测试

本地 URL 模型测试位于 `test/`，覆盖重定向脚本、作用域重叠（equal/contains/contained-by/disjoint）与迟到安装完成：

```sh
node --experimental-strip-types --test "test/*.test.ts"
```
