# DLMS关联等级与远程断开动作授权

攻击者在智能电表上用公共客户端关联后，调用断开控制的 remote_disconnect。为Go电表前置机写可导入的 COSEM 访问判定包：公共客户端（SAP 16）只能读登记的对象；管理客户端（SAP 1）必须用 HLS 完成挑战应答之后，才能对断开控制类（class 70）调用方法 1 或 2；LLS 空口令拒绝；关联释放或重新关联时清除已认证状态。用公共客户端动作、HLS 未完成、空口令和释放后复用验证。

包路径为 `example.org/netsec/q0682`，包名为 `netsec`。

## 访问策略

| 客户端 | Get | Set | class 70 方法 1/2 |
| --- | --- | --- | --- |
| SAP 16 公共客户端 | 仅限已登记对象 | 拒绝 | 拒绝 |
| SAP 1 管理客户端 | 仅限已登记对象 | 拒绝 | 当前关联完成 HLS 后允许 |

- 未关联客户端的任何请求都会返回 `ErrNotAssociated`。
- LLS 只记录口令认证结果；空口令返回 `ErrEmptyPassword`，即使口令正确也不能替代 HLS。
- 每个 HLS 挑战只能使用一次；错误响应、重放和跨关联复用都会失败。
- `Release` 删除会话；再次 `Associate` 会替换会话，二者都会清除 HLS/LLS 状态。
- 默认构造器使用 HMAC-SHA256 验证 HLS，也可通过 `Config.HLSVerifier` 接入设备已有的挑战应答实现。

## 导入示例

```go
package main

import (
	"errors"
	"fmt"

	netsec "example.org/netsec/q0682"
)

func main() {
	secret := []byte("meter-hls-secret")
	controller, err := netsec.NewWithConfig(netsec.Config{
		HLSSecret:   secret,
		LLSPassword: []byte("meter-lls-password"),
		RegisteredObjects: []netsec.ObjectID{
			netsec.NewObjectID(1, [6]byte{0, 0, 1, 0, 0, 255}),
		},
	})
	if err != nil {
		panic(err)
	}

	disconnect := netsec.NewObjectID(
		netsec.ClassDisconnectControl,
		[6]byte{0, 0, 96, 3, 10, 255},
	)

	// 公共客户端不能执行 remote_disconnect。
	_ = controller.Associate(netsec.SAPPublic)
	err = controller.Authorize(netsec.AccessRequest{
		SAP:    netsec.SAPPublic,
		Object: disconnect,
		Access: netsec.AccessAction,
		Method: netsec.MethodRemoteDisconnect,
	})
	fmt.Println("public action:", errors.Is(err, netsec.ErrAccessDenied))

	// 管理客户端先完成 HLS，再执行方法 1。
	_ = controller.Associate(netsec.SAPManagement)
	challenge, err := controller.StartHLS(netsec.SAPManagement)
	if err != nil {
		panic(err)
	}
	response := netsec.HLSResponse(secret, challenge)
	if err := controller.CompleteHLS(netsec.SAPManagement, response); err != nil {
		panic(err)
	}
	err = controller.Authorize(netsec.AccessRequest{
		SAP:    netsec.SAPManagement,
		Object: disconnect,
		Access: netsec.AccessAction,
		Method: netsec.MethodRemoteDisconnect,
	})
	fmt.Println("management action:", err == nil)
}
```

运行验证：

```sh
go test ./...
go vet ./...
```
