# 处方被改成另一患者的药

处方流转平台按处方标识发药，攻击者替换患者标识或药品编码，让药房发出错误药品。请实现供Go处方服务调用的可导入处方绑定组件。处方绑定患者、医师、诊断、药品与剂量并由医师签名，调剂前逐项核对；患者替换、药品替换与超量拒绝。处方状态一次性。用患者替换、药品篡改、重复调剂、合法调剂和签名验证处方只对原患者有效。

## 实现

包 `netsec` 提供可导入的处方绑定与一次性调剂组件：

- `Prescription` 绑定处方标识、患者标识、医师标识、诊断、药品编码、最大剂量、签发/失效时间；`Sign` 使用 Ed25519 对规范化后的全部字段签名。
- `Dispenser` 保存验签通过的处方不可变副本，调剂前重新验签并逐项核对患者、医师、诊断、药品与剂量。
- 患者、医师、诊断、药品任一字段被替换都会拒绝；请求剂量超过签名剂量返回 `ErrOverdose`。
- 一次调剂在同一互斥区内完成“核对 + 状态迁移”，并发重复调剂只有一个成功，其余返回 `ErrAlreadyDispensed`。
- 签名载荷使用域分离与长度前缀编码，签名只对原始患者和其他全部绑定字段有效。

## 使用示例

```go
publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)

rx := netsec.Prescription{
    ID:          "RX-1001",
    PatientID:   "PATIENT-A",
    PhysicianID: "DR-7",
    Diagnosis:   "J18.9",
    DrugCode:    "MED-ASPIRIN-100",
    Dosage:      100,
    Unit:        "mg",
}
if err := rx.Sign(privateKey); err != nil {
    // handle
}

dispenser := netsec.NewDispenser(publicKey)
if err := dispenser.Register(rx); err != nil {
    // invalid signature or inconsistent binding
}

err := dispenser.Dispense(netsec.DispenseRequest{
    PrescriptionID: "RX-1001",
    PatientID:      "PATIENT-A",
    DrugCode:       "MED-ASPIRIN-100",
    Dosage:         100,
})
if errors.Is(err, netsec.ErrPatientMismatch) ||
    errors.Is(err, netsec.ErrDrugMismatch) ||
    errors.Is(err, netsec.ErrOverdose) ||
    errors.Is(err, netsec.ErrAlreadyDispensed) {
    // reject the fill
}
```

`Dispenser.Dispense` 同时接受 `Dispense(prescriptionID, patientID, drugCode, dosage)` 形式；需要调剂凭据时使用 `Fill` 或 `DispenseWithReceipt`。

## 安全属性

- 验签早于任何业务比对，未签名或被篡改的处方不会进入状态表。
- 状态表保存的是注册时的深拷贝，调用方修改原 `Prescription` 不影响已绑定内容。
- 同一处方标识不能用不同内容覆盖；合法的重复导入是幂等的。
- 未登记处方、过期处方、错误医师密钥和未知药品均拒绝。
