# 包身份组件（Package URL Identity）

供 Go 物料清单（SBOM）服务调用的可导入包身份组件。它解析 `pkg:` Package URL，生成稳定的规范化身份，避免攻击者利用大小写、百分号编码、版本范围写法或别名绕过漏洞匹配。

## 能力

- 解析 `type / namespace / name / version / qualifiers / subpath`。
- 按 purl 类型规则规范化大小写：npm、PyPI（PEP 503）、NuGet、Cargo、Go、Docker/OCI、GitHub 等。
- 百分号解码后按规范重新编码，统一 `%40scope%2fname` 与 `@scope/name`。
- 语义化版本比较，并支持 `^`、`~`、`1.x`、`>=1 <2`、`[1.0,2.0)`、连字符区间和 VERS 区间。
- 内置类型别名（`go`→`golang`、`pip`→`pypi`、`node`→`npm` 等），支持注册包坐标别名和 npm `npm:` 依赖别名。
- 未知类型保留原始大小写并设置 `Unknown`/`UnknownType` 标记。

## 使用

```go
package main

import (
	"fmt"

	"example.org/netsec/q0371"
)

func main() {
	a := netsec.MustParse("pkg:NPM/%40Angular/Core@1.0.0")
	b := netsec.MustParse("pkg:npm/@angular/core@1.0.0")

	fmt.Println(a.Identity() == b.Identity()) // true
	fmt.Println(a.String())                   // pkg:npm/%40angular/core@1.0.0

	vuln := netsec.MustParse("pkg:npm/lodash@^4.17.0")
	fmt.Println(vuln.Satisfies("4.17.21")) // true
}
```

也提供两个语义化导入路径：

```go
import (
	purl "example.org/netsec/q0371/purl"
	identity "example.org/netsec/q0371/identity"
)
```

## 关键 API

- `Parse(raw)` / `MustParse(raw)`：解析并规范化 purl。
- `PackageURL.Identity()` / `ID()`：获取不含 `subpath` 的唯一身份；等价版本和区间写法得到相同值。
- `PackageURL.BaseIdentity()`：仅包含类型、命名空间和名称，便于先定位组件再判断版本。
- `PackageURL.Satisfies(version)` / `MatchesVersion(version)`：判断版本是否落在漏洞区间。
- `PackageURL.Matches(other)`：判断两个坐标是否为同一组件且版本相交。
- `CompareVersions(a, b)`、`ParseVersionRange(expr)`、`VersionInRange(version, expr)`：版本与区间工具。
- `RegisterTypeAlias`、`RegisterPackageAlias`：注册企业内部或历史别名。
- `Unknown` / `UnknownType`：未知类型标记，避免对未知生态误套用大小写规则。
