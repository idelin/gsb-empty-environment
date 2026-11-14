# RBAC只限制了pods却没限制pods/exec

集群授权给值班账号`pods`读权限，但未显式拒绝`pods/exec`子资源，攻击者用exec进入任意容器读取挂载凭据。请实现供Go集群授权服务调用的可导入子资源判定组件。每条规则声明资源、子资源、动词与资源名，授权时按完全匹配求值，未声明子资源默认拒绝；`pods/exec`、`pods/attach`、`pods/portforward`与日志子资源分别建模。聚合与通配只在显式写出时生效，跨命名空间与API组不得借用。用exec子资源、attach、通配规则、跨命名空间、合法pod读取和显式exec授权验证未声明能力不可用。

## 使用方式

```go
policy := netsec.New([]netsec.Rule{
	{
		Namespace:    "prod",
		Resource:     netsec.ResourcePods,
		Verb:         netsec.VerbGet,
		ResourceName: "web-0",
	},
	{
		Namespace:    "prod",
		Resource:     netsec.ResourcePods,
		Subresource:  netsec.SubresourceExec,
		Verb:         netsec.VerbCreate,
		ResourceName: "web-0",
	},
})

ok := policy.Authorize(netsec.Request{
	Namespace:    "prod",
	Resource:     netsec.ResourcePods,
	Subresource:  netsec.SubresourceExec,
	Verb:         netsec.VerbCreate,
	ResourceName: "web-0",
})
```

## 判定约定

- 每个维度默认完全匹配：API 组、命名空间、资源、子资源、动词、资源名。
- 空字段不是通配符。规则没有声明子资源时，只匹配无子资源的基础资源请求；`pods` 的读权限不会覆盖 `pods/exec`。
- 只有显式写入 `netsec.Wildcard`（`"*"`）的维度才按通配处理。子资源通配覆盖具体子资源，但不隐式扩展到基础资源。
- `pods/exec`、`pods/attach`、`pods/portforward`、`pods/log` 分别使用 `SubresourceExec`、`SubresourceAttach`、`SubresourcePortForward`、`SubresourceLog` 声明。
- 命名空间与 API 组是独立维度，规则不能跨命名空间或跨 API 组借用权限。
- 没有任何匹配规则时默认拒绝；非法规则不会匹配。

`Resource` 也可以写成 `"pods/exec"` 这样的组合形式，但不能同时再设置 `Subresource`，以免产生歧义。
