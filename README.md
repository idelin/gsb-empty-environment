# 数组元素披露位置

数组里的披露对象被移动到另一个下标仍通过摘要。数组披露按摘要对应元素，不按攻击者给的新下标重新解释；下标不连续则失败。按原下标披露的数组接受。可导入的Go库供验证组件调用。交换两个已披露元素的位置。

## 安全语义

签名侧数组按原始顺序保存每个元素的摘要，披露侧只携带 `index`、`salt` 和 `value`。验证组件执行以下检查：

- 先计算披露的摘要，再在签名数组中查找该摘要，得到唯一原下标。
- 绝不使用调用方给出的 `index` 选择数组位置；声明下标与摘要推导出的下标不一致即失败。
- 两个披露解析到同一位置、摘要重复或摘要无法匹配均失败。
- 默认要求披露位置构成连续区间；位置断档返回 `ErrNonContiguousIndices`。
- 原始下标完整披露可通过；交换两个披露的声明下标会返回 `ErrIndexMismatch`。

## 用法

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0174"
)

func main() {
	commitment, disclosures := loadPresentation()
	values, err := netsec.VerifyArray(commitment, disclosures)
	if err != nil {
		panic(err)
	}
	fmt.Println(values)
}
```

核心入口是 `VerifyArray(commitment []string, disclosures []Disclosure)`，返回按签名顺序重建的数组。只需要校验结果时可调用 `ValidateArray`。原始编码披露可使用 `VerifyDisclosures`；带显式选项时使用 `VerifyArrayWithOptions`。

当协议本身允许稀疏披露时，可显式设置 `Options{AllowSparse: true}`。默认策略保持严格，避免被移动的披露留下断档后仍被接受。
