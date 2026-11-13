# 点不在曲线上

坐标满足长度但落在扭曲线上，交换仍继续。点必须满足曲线方程；不满足则失败，不能只检查长度。曲线上的点可以参与交换。可导入的Go库供TLS栈调用。改动y坐标使它离开曲线。

## 修复方案

`netsec` 是一个可导入的 P-256 ECDH 库。收到对端公钥点时，先显式校验曲线
方程 `y^2 == x^3 - 3x + b (mod p)`，而不是只检查字节长度。长度正确但不在
曲线上的点（例如被改动 `y` 坐标的点）会返回 `ErrInvalidPoint`，交换立即
失败；曲线上的点正常完成密钥交换。

## 用法

```go
import "example.org/netsec/q0234"

local, _ := netsec.GenerateKey()

// peerPoint 为对端的未压缩点 0x04 || x || y。
// 内部先做曲线方程校验，再计算共享密钥。
secret, err := local.SharedSecret(peerPoint)
if errors.Is(err, netsec.ErrInvalidPoint) {
    // 点长度正确但不在曲线上，拒绝本次交换。
}

// 也可单独使用校验入口。
if err := netsec.ValidatePeerPoint(peerPoint); err != nil {
    // 非法/离曲线点。
}
```

## 测试

`go test ./...` 覆盖：合法点双方得到相同共享密钥、篡改 `y` 后的等长离曲线
点被拒绝、畸形编码被拒绝，以及 `y` 取反仍在曲线上等边界情况。
