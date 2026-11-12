package pseudonym_test

import (
	"fmt"

	"example.org/netsec/q0448/pseudonym"
)

// Example 展示脱敏服务的典型调用：分配伪名、携带完整 HMAC 写事件、
// 在碰撞短码上查询并按完整摘要隔离不同身份的事件。
func Example() {
	// 生产环境的 HMAC 密钥应来自密钥管理系统并长期固定，切勿硬编码。
	allocator, err := pseudonym.New(
		pseudonym.NewMemoryStore(),
		pseudonym.NewHMACDigester([]byte("persistent-server-secret")),
	)
	if err != nil {
		panic(err)
	}

	code, err := allocator.Allocate("user-1001")
	if err != nil {
		panic(err)
	}
	digest, err := allocator.Digest("user-1001")
	if err != nil {
		panic(err)
	}

	// 事件以短码为索引，但同时保存完整 HMAC 作为身份凭据。
	event := struct {
		code   string
		digest []byte
		action string
	}{code: code, digest: digest, action: "login"}

	// 查询时先用短码拿到所有可能的身份，再用完整摘要确认，避免碰撞误并。
	candidates, err := allocator.Resolve(event.code)
	if err != nil {
		panic(err)
	}
	owner := ""
	for _, identity := range candidates {
		want, _ := allocator.Digest(identity)
		if allocator.Confirm(event.code, want) {
			owner = identity
			break
		}
	}

	fmt.Printf("published code length: %d\n", len(event.code))
	fmt.Printf("resolved candidates: %d\n", len(candidates))
	fmt.Printf("confirmed owner: %s\n", owner)
	// Output:
	// published code length: 8
	// resolved candidates: 1
	// confirmed owner: user-1001
}
