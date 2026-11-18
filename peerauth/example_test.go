package peerauth_test

import (
	"errors"
	"fmt"

	"example.org/netsec/q0528/peerauth"
)

// evaluator 是生产环境中对 SecCode/SecStaticCode 代码要求判定的封装位置：
// 每次调用都按审计令牌实时取对端进程并校验团队 ID、签名标识与平台标记，
// 绝不缓存连接建立时的结果。
func evaluator(token peerauth.AuditToken) (peerauth.Identity, error) {
	return peerauth.Identity{}, errors.New("wire to Security framework SecCode checks")
}

func Example() {
	validator, err := peerauth.NewValidator(
		evaluator,
		peerauth.Grant{
			TeamID:    "TEAMGOOD",
			SigningID: "com.example.agent",
			AuthKeys:  []string{"agent.read", "agent.write"},
		},
		peerauth.Grant{
			TeamID:         "",
			SigningID:      "com.apple.platform.syspolicy",
			PlatformBinary: true,
			AuthKeys:       []string{"platform.policy"},
		},
	)
	if err != nil {
		panic(err)
	}

	// XPC listener 的新连接回调：用连接的审计令牌做准入判定。
	token := peerauth.AuditToken{Pid: 100, PidVersion: 1, Euid: 501}
	if err := validator.ValidateConnection(token); err != nil {
		fmt.Println("reject connection:", err)
		return
	}

	// 每条消息的处理回调：消息所需的授权键按当前令牌的身份实时核对。
	if err := validator.Authorize(token, "agent.write"); err != nil {
		fmt.Println("reject message:", err)
		return
	}

	// 进程生命周期事件接到后直接按 pid 清除相关缓存。
	validator.NotifyExec(token.Pid)
	validator.NotifyExit(token.Pid)

	fmt.Println("validator ready")
	// Output: reject connection: peerauth: evaluate audit-token(pid=100,pidversion=1,euid=501): wire to Security framework SecCode checks
}
