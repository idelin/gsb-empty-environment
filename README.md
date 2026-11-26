BGP 路由优选默认按到达顺序两两比较同邻域 AS 的 MED 时，三组候选路径极易因非传递性引发永久路由震荡；同时路由反射器（RR）的 `ORIGINATOR_ID`/`CLUSTER_LIST` 防环规则与联邦（RFC 5065）内部 `AS_CONFED_SEQUENCE`/`AS_CONFED_SET` 长度计零规则若处理有误，就会造成路由黑洞或环路。为控制面路由进程交付可导入的 Go BGP 最佳路径选择与反射发布包。输入为本地路由器配置（本地 AS、成员 AS、RouterID、ClusterID、是否启用 `deterministic-med` 与 `always-compare-med`、缺失 MED 默认 0 或无穷大策略）及某前缀的候选路径表（每路径含 `LOCAL_PREF`、由 `AS_SEQUENCE`/`AS_SET`/`AS_CONFED_SEQUENCE`/`AS_CONFED_SET` 组成的 `AS_PATH`、`ORIGIN`、`MED`、下一跳可达性与 IGP 代价、学习来源 eBGP/confed-eBGP/iBGP、`ORIGINATOR_ID`、`CLUSTER_LIST`、对端 RouterID 与 IP）。规则：先执行合法性过滤（丢弃下一跳不可达、AS_PATH 含本 AS、ORIGINATOR_ID 等于本机 RouterID 或 CLUSTER_LIST 含本机 ClusterID 的路径）；AS_PATH 长度按标准计费（`AS_SET` 计 1、`AS_CONFED_*` 计 0）；按邻居首个非联邦 AS 分组先在组内选出唯一胜出路径（确定性 MED，彻底消除到达顺序依赖！），再跨组依序比较 `LOCAL_PREF`（大优先）、AS_PATH 长度（短优先）、`ORIGIN`（IGP<EGP<INCOMPLETE）、跨组 MED（仅当启用 `always-compare-med` 时）、路径类型（eBGP 优于 confed-eBGP 优于 iBGP）、IGP 代价（小优先）、`CLUSTER_LIST` 长度（短优先）、有效 `ORIGINATOR_ID`/对端 RouterID（小优先）及最低对端 IP；并生成向 iBGP 客户/非客户与 eBGP 邻居通告时按 RFC 4456 改写 `ORIGINATOR_ID` 和前置 `CLUSTER_LIST` 的发布结果（不将非客户 iBGP 路由反射给非客户）。验收覆盖经典三路径 MED 震荡在任意排列输入下结果严格一致、联邦段计零与首个非联邦邻居 AS 提取、RR 防环过滤与客户/非客户反射矩阵。

## 交付内容

可导入包 `prescreen.local/ps-90d70352b5/bgp`，无第三方依赖，仅用标准库：

- `bgp/types.go`：配置、候选路径、AS_PATH 段、拒绝原因等类型。
- `bgp/path.go`：合法性过滤、AS_PATH 计费、邻居 AS 提取、RouterID/IP 工具。
- `bgp/select.go`：确定性 MED 选择器（`Selector.Select`）与全序比较器。
- `bgp/reflect.go`：RFC 4456 反射矩阵与出向属性改写（`Selector.Reflect`）。

## 关键行为

- **确定性 MED**：按首个非联邦邻居 AS 分组，组内始终比较 MED 并选出唯一胜者，
  再按升序组键跨组比较；任意输入排列结果严格一致。关闭 `DeterministicMED` 时
  保留到达顺序敏感的传统两两折叠算法，用于对照复现震荡。
- **联邦计零**：`AS_CONFED_SEQUENCE`/`AS_CONFED_SET` 不计 AS_PATH 长度，
  提取邻居 AS 时跳过；成员 AS 出现在联邦段中按联邦环路拒绝。
- **RR 防环**：`ORIGINATOR_ID == 本机 RouterID` 或 `CLUSTER_LIST` 含本机
  `ClusterID` 的路径直接过滤；反射时前置本机 ClusterID 并保留 ORIGINATOR_ID。
- **反射矩阵**：非客户 iBGP 路由只反射给客户，绝不反射给非客户；客户路由可
  反射给客户与非客户；eBGP 发送剥离 RR 属性、隐藏联邦段并前置本地 AS。

## 快速使用

```go
package main

import (
	"fmt"

	"prescreen.local/ps-90d70352b5/bgp"
)

func main() {
	routerID, _ := bgp.ParseRouterID("10.0.0.1")
	clusterID, _ := bgp.ParseRouterID("10.0.0.2")

	sel := bgp.NewSelector(bgp.Config{
		LocalAS: 65000, MemberAS: 64512,
		RouterID: routerID, ClusterID: clusterID,
		DeterministicMED: true,
		MissingMED:       bgp.MissingMEDAsZero,
	})

	result := sel.Select(candidates) // candidates: []*bgp.Path
	if result.Best == nil {
		for _, rejected := range result.Rejected {
			fmt.Println("rejected:", rejected.Path.ID, rejected.Reason)
		}
		return
	}

	peers := []bgp.Target{
		{PeerID: "client-1", Role: bgp.RoleIBGPClient},
		{PeerID: "nonclient-1", Role: bgp.RoleIBGPNonClient},
		{PeerID: "external-1", Role: bgp.RoleEBGP},
	}
	for _, entry := range sel.Reflect(result.Best, peers).Entries {
		if entry.Advert == nil {
			fmt.Println("suppressed:", entry.Target.PeerID, entry.Reason)
			continue
		}
		fmt.Println("advertise to", entry.Target.PeerID)
	}
}
```

## 测试

```sh
go test ./...                 # 全部验收用例
go test -race ./bgp/         # 竞态检测
go test ./bgp/ -coverprofile=cov.out && go tool cover -func=cov.out
```

验收测试覆盖：经典三路径 MED 非传递震荡的全部 6 种排列 + 50 次随机洗牌结果一致、
传统模式确实随到达顺序变化、`always-compare-med` 跨组 MED、联邦段计零与邻居 AS
提取、五类合法性过滤原因、完整 tie-break 顺序，以及 RR 客户/非客户/eBGP 反射矩阵。
