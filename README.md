# 可变时间库调用契约

安全审查需要判断组合函数是否保持调用方声明的恒定时间契约。写供Go分析宿主调用的摘要检查包，调用节点声明耗时依赖的参数位与允许公开位，位运算、选择和查表传播依赖；公开掩码不能消除仍影响地址的秘密位。返回违反调用契约的位级路径，用人工构造的库摘要和差分执行核对，不声称检测物理硬件泄漏。

## 用法

宿主把组合函数表示为直线 IR（`Program`），并为库函数人工构造 `Summary`：

```go
libraries := netsec.Libraries{
	"ctcmp": {
		Name: "ctcmp",
		Params: []netsec.ParamSummary{{
			TimingBits: 0xff, // 该参数的这些位可能影响库函数耗时
		}},
	},
}

builder := netsec.NewBuilder("compare-secret")
secret := builder.Secret("secret", 8)
mask := builder.Const("low-nibble", 8, 0x0f)
masked := builder.And("masked", secret, mask)
builder.Call("compare", netsec.CallSpec{
	Library:       "ctcmp",
	AllowedPublic: []netsec.Mask{0x0f}, // 调用方只允许低 4 位影响耗时
}, masked)
program := builder.Build()

report, err := netsec.Analyze(program, libraries)
```

`report.Findings` 中每条记录包含：

- `Kind`：`timing-contract`、`secret-address` 或 `secret-branch`。
- 发生违约的调用/查表/分支节点与参数位。
- 起始秘密输入位 `Source`。
- `Path`：从秘密位到汇点的逐位操作路径。

## 传播规则

- `And`、`Or`、`Xor`、`Not`、移位、加减乘均按位传播来源；常量位会剪枝。
- `Select` 把条件、真值和假值的来源传播到结果。选择同一个值时不会仅因条件而污染结果。
- `Lookup` 的结果依赖索引和表内容；索引位的秘密来源会形成 `secret-address`。
- `Call` 用 `Summary` 的 `TimingBits` 检查耗时依赖，用 `AddressBits` 检查地址依赖。
- `AllowedPublic` / `PublicBits` / `OpPublicMask` 可对普通耗时检查解除秘密标记，但地址检查始终使用原始秘密来源，因此“公开掩码”不能洗掉仍影响地址的秘密位。只有显式设置 `AllowPublicAddress` 才放宽该规则。

## 差分执行

`Execute` 对同一 IR 做确定性具体执行，记录查表地址、分支条件和库调用的耗时/地址签名。`Differential` 翻转一个秘密输入位，比较两次事件轨迹；`DifferentialAll` 遍历全部位。测试中的手工库摘要和差分执行用于核对静态路径与可观察差异。

差分执行只说明两次运行的可观察事件是否变化，不单独裁决契约：落在 `AllowedPublic` 位上的差异可以变化，但属于调用方允许的公开范围，需要和 `Findings` 一起解释。

## 边界

该包只分析软件数据流和调用方声明的契约，不测量也不声称检测物理硬件泄漏（缓存竞争、推测执行、功耗、电磁等）。值宽度上限为 64 位；更宽的状态应由宿主拆成多个 64 位节点。
