# cors-replay — 浏览器侧 CORS 策略离线复盘

纯标准库 Go 程序。输入请求序列、凭据模式、方法与头、预检响应和实际响应，
在虚拟时钟上重放浏览器的 CORS 判定，逐次区分两件事：

1. **请求能否发出** —— 跨源非简单请求先做预检；预检失败时实际请求不会发出。
2. **脚本能否读回响应** —— 请求即使已经到达服务器，响应头不过 CORS 检查时，
   脚本只能拿到不透明响应（opaque response）。

## 运行

```bash
# 内置演示场景（三组，见下文）
go run . 

# 结构化结果
go run . -json

# 自定义输入
go run . -input my-cases.json

# 导出内置场景的 JSON 模板
go run . -write-example > example-input.json
```

若环境缺少 Git（VCS stamping 报错），加 `-buildvcs=false`。

## 输入模型

顶层对象：

| 字段 | 说明 |
| --- | --- |
| `preflight_enforcement` | `modern`（默认，Fetch 现行规范）或 `legacy`（旧浏览器非凭据预检不强制 ACAO） |
| `max_age_cap_seconds` | 裁剪 `Access-Control-Max-Age`，默认 7200；显式 `0` 表示不裁剪 |
| `routes[]` | 服务端行为建模，按 `origin` + `path`（支持 `/*` 前缀）匹配 |
| `requests[]` | 按 `at_seconds` 排序重放的请求序列 |

`routes[].preflight` 描述 OPTIONS 预检响应，`changes[]` 可让预检响应随虚拟时间变化
（用于“预检缓存过期后服务端变脸”）；`routes[].reply` 描述实际响应，
`location` 非空且状态为 3xx 时触发重定向。

`requests[]` 字段：`source`（发起源，字符串 `null` 表示不透明源）、`url`、`method`、
`headers`、`credentials`（`omit`/`include`）、`note`。

## 实现的规范要点

- **预检触发条件**：方法不在 GET/HEAD/POST 内，或含非安全头，或
  `Content-Type` 媒体类型不是 `application/x-www-form-urlencoded`、
  `multipart/form-data`、`text/plain`。禁止修改的协议头（`Origin`、`Cookie` 等）不参与判定。
- **预检校验**：2xx 状态；ACAO 匹配发起源（`modern` 下始终强制）；
  凭据模式要求 `Access-Control-Allow-Credentials: true` 且 ACAO 不得为 `*`；
  ACAM 必须覆盖方法、ACAH 必须覆盖每个请求头。凭据预检中 ACAM/ACAH 的 `*`
  不能代替显式列举（对应 Chrome 144+ 的收紧行为）。
- **预检缓存隔离**：缓存键严格为
  `(发起源, 目标 URL 去掉 fragment, 凭据模式)`，条目内记录已授权方法/头集合；
  方法或头超出集合同样触发重新预检。`Max-Age` 到期惰性失效，无 `Max-Age` 视为会话级。
- **实际响应校验**：ACAO 匹配（凭据模式拒绝 `*`、要求 ACAC）；
  可读头 = CORS 安全响应头 ∪ `Access-Control-Expose-Headers`，
  凭据模式下 `*` 不暴露 `Authorization`。
- **重定向**：按 Fetch 的“实际请求循环”逐跳处理，每跳独立判定同源/跨源与预检；
  301/302/303 改写为 GET（HEAD 除外），307/308 保留方法，所有状态保留请求头；
  中间跳转不做 CORS 检查，只对终态响应做检查；发起源在整条链路中保持不变。
  预检请求本身收到重定向按失败处理。

## 三组演示场景如何分辨两步判定

**场景一 `w*`：通配源 + 凭据**

- `w1` 无凭据简单 GET + `ACAO: *` → 发出且可读。
- `w2` 带凭据 PUT + 自定义头 → 预检阶段即失败，**请求未发出**。
- `w3` 带凭据简单 GET → 无预检、请求已到达服务器，但 `ACAO: *` 配合凭据使响应不透明，
  **已发出但不可读**。与 `w2` 对照可区分“预检拦截”和“响应不透明”。
- `w4/w5` 不透明源：必须显式回显 `null`，通配符不匹配。

**场景二 `p*`：预检过期 + 缓存键隔离**

- `p1` 预检通过并缓存（Max-Age=10），`p2` 缓存命中不发 OPTIONS，
  `p3` 在 t=22 过期后重新预检，`p4` 在服务端 t=30 变脸撤权 `x-custom` 后再次预检失败。
- `p5` 换发起源、`p6` 换凭据模式、`p8` 换目标 URL，均产生独立缓存条目；
  `p7` 新头超出条目授权集合，同样强制重新预检。

**场景三 `r*`：重定向换源**

- `r1` 简单 GET 跟随 302 到 CDN，中间跳转不检查 CORS，终态 ACAO 匹配 → 可读。
- `r2` 307 保留 PUT 与自定义头：edge 预检通过后，CDN 一跳仍需独立预检，缺失即阻断。
- `r3` 303 改 GET 但保留请求头，CDN 上 GET + JSON content-type 仍触发预检。
- `r4` 首跳同源免 CORS，换源后发起源仍是 edge，终态 ACAO 不匹配 → 已发出但不透明。

## 代码结构

```
main.go                  CLI 入口
internal/cors/
  model.go               输入/路由/请求数据模型
  policy.go              源解析、简单请求判定、预检与实际响应校验
  cache.go               按 源/URL/凭据 隔离的预检缓存与虚拟时钟
  engine.go              逐跳重放：预检 → 实际请求 → 重定向 → CORS 终检
  demo.go                三组内置场景
  report.go              文本报告
```

测试：`go test ./...`，覆盖规则函数、缓存隔离/过期、预检拦截 vs 响应不透明、
重定向逐跳预检、303 方法改写和同源豁免。
