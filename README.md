# DICOM 影像导出匿名化组件

`example.org/netsec/q0379` 提供一个供 Go 影像导出服务导入的 DICOM
去标识化组件。它直接处理 Part 10 文件或裸 DICOM 数据集，不依赖外部
DICOM 工具。

## 使用

同一批导出应复用同一个 `Anonymizer`，或者显式传入固定的 `Secret`，这样
跨文件的 Study/Series/Instance UID 引用和同患者日期偏移保持一致。

```go
package export

import (
    "log"

    netsec "example.org/netsec/q0379"
)

func anonymizeExport(secret []byte) error {
    anonymizer, err := netsec.NewAnonymizer(
        netsec.WithSecret(secret),
        netsec.WithUIDRoot("1.2.826.0.1.3680043.10.999"),
    )
    if err != nil {
        return err
    }

    // 同一 anonymizer 处理整个导出批次。
    if err := anonymizer.AnonymizeFile("input.dcm", "output.dcm"); err != nil {
        return err
    }
    return nil
}
```

如果私有标签是明确允许保留的非身份字段，可按完整标签白名单放行：

```go
anonymizer, err := netsec.NewAnonymizer(
    netsec.WithSecret(secret),
    netsec.WithPrivateTagWhitelistSpecs("0019,1001", "(0019,1002)"),
)
```

白名单只影响列出的私有标签；为使保留值仍可解释，对应的私有创建者标识
会一并保留。白名单中的私有序列仍会递归清理，私有的姓名、UID 和日期
仍分别按 VR 清空或替换。未列入白名单的私有标签默认全部移除。

流式调用可使用 `AnonymizeReader` / `AnonymizeStream`；单次字节调用可使用
`Anonymize` 或 `AnonymizeDICOM`。

## 安全行为

- 标准患者身份字段（姓名、ID、检查号、地址、电话、机构、描述、注释等）
  被清空；`Modality`、`BodyPartExamined` 和像素数据等非身份字段保留。
- 所有实例类 UID（Study、Series、SOP Instance、Frame of Reference、
  Referenced SOP Instance 等）使用 HMAC 派生的新 UID，并在文件内、
  嵌套序列内和同一 `Anonymizer` 的跨文件引用间保持一致。SOP Class UID、
  Transfer Syntax UID 等类型标识不会被改写。
- `DA`/`DT` 日期按患者做不可逆的确定性日期偏移，保留日期区间和日期时间
  关系；不同患者即使原始日期相同也不会得到相同偏移。
- 私有标签按标签字典和 VR 处理；奇数分组标签默认删除，包括嵌套序列、
  Icon Image 和文件元信息中的私有内容。
- 图形注释、文本对象、覆盖层/曲线数据和常用注释字段被逐层清理。
- 128 字节文件前导区默认清零，避免残留任意数据；文件元信息组长度会重算。
- 输入声明 `BurnedInAnnotation=YES` 时默认返回 `ErrBurnedInAnnotation`。
  元数据清理无法移除已烧录进像素的姓名，调用方必须先做像素脱敏，或在
  明确接受风险时使用 `WithRejectBurnedInAnnotation(false)`。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖私有标签残留、嵌套序列、UID 跨文件关联、同患者/不同患者日期
偏移、白名单合法保留、注释/覆盖层清理、显式/隐式 VR、大端、Deflate
以及匿名化结果复导入。
