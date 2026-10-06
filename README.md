# verisched

Go 验证调度库：按冻结包调度检查回执，并据此判定发布门禁。

## 核心语义

- **冻结包（Package）**：`FreezePackage(inputDigest, checks)` 将输入摘要与全部检查
  （含验证器版本）哈希为包 id。输入或任一规则版本变化都会生成新包，旧包自动归档。
- **结果绑定**：每张回执（Receipt）绑定包 id、输入摘要、验证器版本和运行 id，
  四者任一不匹配即拒绝（`ErrBindingMismatch` / `ErrUnknownRun`）。
- **归档只读**：归档包的回执仅保留用于审计（`ErrPackageArchived`），
  永远不能满足新门禁；旧运行 id 在新包下失效（`ErrUnknownRun`）。
- **同一冻结包**：门禁按“当前包 × 单次运行”评估，包内所有检查全部通过才放行；
  不同运行的回执不能混用。
- **撤销**：`RevokeCheck` 使该检查已有回执失效并拒绝新回执，门禁随即关闭。

## 用法

```go
s := verisched.NewScheduler()
pkg, _ := s.FreezePackage("sha256:...", []verisched.CheckSpec{
    {ID: "lint", Validator: "lintval", Version: "1.0.0"},
    {ID: "unit", Validator: "testval", Version: "2.1.0"},
})
run, _ := s.StartRun()
_ = s.Submit(verisched.Receipt{
    PackageID: pkg.ID, CheckID: "lint", RunID: run.ID,
    InputDigest: pkg.InputDigest, ValidatorVersion: "1.0.0",
    Status: verisched.StatusPassed,
})
rep, _ := s.Gate(run.ID) // rep.Passed 仅在包内全部检查通过时为 true
```

回执可以乱序到达；重复回执返回 `ErrDuplicateReceipt`。

## 测试

```sh
GOCACHE=/private/tmp/gocache go test ./...
```

覆盖：乱序回执、部分通过、撤销检查、输入/规则变化后的错误复用、
跨运行混用、重复回执、冻结顺序无关性、未知包/检查/运行。
