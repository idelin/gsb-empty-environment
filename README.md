发布服务接受一组已验签的构建收据，但一份干净制品的名字可能在后续步骤里被同名不同内容替换。给 Go 发布门禁写可导入的制品流核验包。每个步骤声明输入和输出的逻辑路径、摘要、是否消耗输入、允许的重命名规则与签署者角色。消耗型输入只能沿同一构建实例的一条边使用一次；可共享输入可以分叉，但任何派生都须保留其签名来源。规则可按完整路径匹配，或用一个捕获分量在输入输出间建立同名约束；名字规范化只允许消除明确的当前目录分量，不能跨越租户根。收据可乱序，步骤有可选替代实现，不能把取自不同替代实现的两个输出拼成同一次构建。最多16个步骤，每步3个候选；必须判定最终清单是否存在一组全局一致的签名收据与制品对应关系，并检查所有可行对应关系是否都给最终敏感产物相同的根来源集合；存在两种归属就拒绝含糊授权。提供可独立验真的成功证书及歧义时的两组来源标识，无须重跑构建。用小收据集穷举校验同摘要双来源、一次性输入被两步同时消耗、捕获路径交叉、替代实现的部分输出和仅看单步全部合法但整图无对应的情况。

## artifactflow 包

入口为 `artifactflow.Verify(*Problem) (*Report, error)`，收据假定已在外部完成密码学验签。

- `Report.Status` 取值 `ok` / `ambiguous` / `infeasible`。
- `ok` 时 `Report.Certificate` 为自包含成功证书，可用 `VerifyCertificate` 独立复验，无需重跑构建；可用 `HMACGate` 或自定义 `GateVerifier` 附加/复验门禁签名。
- `ambiguous` 时 `Report.Ambiguity` 给出冲突的敏感产物路径、两组根来源标识及两份完整见证 `Assignment`。
- 步骤最多 16 个、每步候选最多 3 个；`Slot.Consumed` 控制一次性输入，`Rule` 支持完整路径与 `{name}` 捕获分量；`NormalizePath` 只消除显式 `.` 分量并拒绝跨越租户根。

最小用法：

```go
report, err := artifactflow.Verify(&artifactflow.Problem{
    TenantRoot: "tenant",
    Receipts: []artifactflow.Receipt{
        {ID: "root", Path: "tenant/in/a", Digest: digestA, Signer: "vendor", RootSource: "src-1"},
        {ID: "out", Path: "tenant/out/f", Digest: digestF, Signer: "builder", StepID: "build"},
    },
    Steps: []artifactflow.Step{{
        ID: "build",
        Candidates: []artifactflow.Candidate{{
            Name:    "c",
            Inputs:  []artifactflow.Slot{{Name: "in", Path: "tenant/in/a", Digest: digestA}},
            Outputs: []artifactflow.Slot{{Name: "out", Path: "tenant/out/f", Digest: digestF}},
            Rules:   []artifactflow.Rule{{Input: "in", Output: "out"}},
        }},
    }},
    Manifest: []artifactflow.ManifestEntry{{Path: "tenant/out/f", Digest: digestF, Sensitive: true}},
})
```

测试覆盖同摘要双来源歧义、一次性输入双步消耗、捕获路径交叉、替代实现部分输出拼接、单步合法但整图无对应、同名不同内容替换、路径规范化与越界、签署者角色、乱序收据、独立证书复验与防篡改，并用独立朴素穷举求解器做差分校验。运行：`go test ./...`。
