# Buildah 构建运行选项准入

`netsec` 是供构建请求准入调用的 Go 校验包。它对不可信构建采用默认拒绝策略，并在一个错误中返回全部违规项及稳定代码。

## 校验规则

- 不可信构建仅允许 `oci` 或 `rootless` 隔离；`chroot` 仅可在调用方明确置 `Trusted` 后使用。
- `Network`、`PID`、`IPC` 不得为 `host`。外部命名空间路径或容器共享命名空间因无法证明不指向宿主，也会被拒绝。
- 新增能力必须位于 `Policy.AllowedCapabilities`；`SYS_ADMIN`、`SYS_PTRACE`、`SYS_MODULE` 和 `ALL` 即使登记也会被硬拒绝。
- 宿主挂载源必须位于 `Policy.HostMountRoots`，且符号链接解析后的真实路径仍须位于白名单。挂载选项必须同时包含 `ro`、`nosuid`、`nodev`，并拒绝 `rw`、`suid`、`dev` 及不安全传播选项。
- 设备路径必须精确匹配 `Policy.AllowedDevices` 中的登记项。
- 用户映射的宿主 ID 范围不得包含宿主 `0`。

## 接入示例

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0665"
)

func admit(req netsec.BuildRequest) error {
	policy := netsec.Policy{
		AllowedCapabilities: []string{"CAP_NET_BIND_SERVICE"},
		HostMountRoots:      []string{"/var/lib/build-cache"},
		AllowedDevices:      []string{"/dev/null"},
	}

	// Trusted 必须由准入服务在认证、授权后设置，不能取自客户端原始请求。
	err := netsec.Validate(req, policy)
	if err != nil {
		admissionErr, ok := err.(*netsec.ValidationError)
		if ok {
			for _, violation := range admissionErr.Violations {
				fmt.Printf("field=%s code=%s message=%s\n",
					violation.Field, violation.Code, violation.Message)
			}
		}
		return err
	}
	return nil
}
```

若服务只处理不可信构建，可直接调用 `netsec.ValidateUntrusted(req, policy)`，该函数会忽略请求中的 `Trusted` 字段。

## 安全注意

- `Trusted` 是准入方输入的信任结论，不是客户端可控字段。
- 挂载白名单目录与挂载源在准入时必须存在。校验通过后，执行层应使用解析后的路径并在真正挂载前重新确认，避免符号链接在检查与使用之间被替换。
- `Policy` 中不存在、非绝对路径或不是目录的挂载根会以 `policy_invalid` 拒绝相关请求；设备登记必须使用绝对路径。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖合规的 `oci`/`rootless` 请求，以及 `chroot`、主机网络、PID/IPC 宿主共享、外部命名空间、`SYS_ADMIN`、直接和符号链接挂载逃逸、不安全挂载选项、未登记设备和宿主 root 用户映射。
