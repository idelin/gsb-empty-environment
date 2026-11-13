# hostkeys — OpenSSH 主机密钥轮换通告的安全处理

`hostkeys` 是可导入的 Go 包，处理 OpenSSH 的
`hostkeys-00@openssh.com` 通告与 `hostkeys-prove-00@openssh.com` 持有证明，
防止已通过认证的恶意服务器把任意公钥写入 `known_hosts`。

## 安全策略

- 仅在 `HostKeyCallback` 成功（主机认证完成）后才处理通告。
- 仅当 `known_hosts` 中已存在该主机条目时才处理；未知主机不会被“教会”新密钥。
- 对通告中的每个公钥发送 prove 请求，要求服务器对
  `session_id || public_key_blob` 给出签名，逐钥用对应公钥验证。
- 任一证明缺失、格式错误、绑定到其它会话或验签失败，整批丢弃，不做任何写入。
- 仅当整批证明全部成功后才原子重写 `known_hosts`：
  新证明的密钥被追加，服务器不再列出的旧条目才被移除。

## 用法

```go
import (
    "golang.org/x/crypto/ssh"
    "golang.org/x/crypto/ssh/knownhosts"

    "example.org/netsec/q0814/hostkeys"
)

func dial(addr string) (*ssh.Client, error) {
    rotator, err := hostkeys.New(hostkeys.Options{KnownHostsPath: "/path/to/known_hosts"})
    if err != nil {
        return nil, err
    }

    callback, err := knownhosts.New("/path/to/known_hosts")
    if err != nil {
        return nil, err
    }

    cfg := &ssh.ClientConfig{
        HostKeyCallback: rotator.WrapCallback(callback),
    }
    return rotator.Dial("tcp", addr, cfg)
}
```

也可以用 `rotator.Config(cfg)` 包装既有配置，或用
`rotator.NewClientConn` 在自定义连接上安装全局请求拦截器。

## 测试

`rotation_test.go` 使用进程内 SSH 服务器覆盖：

- 成功轮换：新证明密钥写入、旧条目保留/移除；
- 无证明：服务器拒绝 prove，整批不写入；
- 签名属于别的会话：验签失败，整批不写入；
- 部分失败：一批中一个签名被篡改，全部密钥都不落盘；
- 未知主机与畸形通告被忽略/中止。
