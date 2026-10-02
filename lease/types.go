package lease

import "time"

// ReadArgs 是客户端请求读租约的参数。
type ReadArgs struct {
	Path         string // 文件路径
	CallbackAddr string // 客户端回调地址，服务端撤销租约时通过它通知客户端
}

// ReadReply 返回文件内容以及租约信息。
// 在 ExpiresAt 之前，客户端可以绝对放心地读取本地缓存，
// 服务端保证在此之前不会有任何写入生效。
type ReadReply struct {
	Content   []byte
	Version   uint64
	LeaseID   string
	ExpiresAt time.Time
}

// WriteArgs 是写客户端修改文件的参数。
type WriteArgs struct {
	Path    string
	Content []byte
}

// WriteReply 返回写入后的新版本号。
type WriteReply struct {
	Version uint64
}

// RevokeArgs 是服务端回调客户端撤销租约的参数。
type RevokeArgs struct {
	LeaseID string
}

// RevokeReply 客户端确认已失效本地缓存。
type RevokeReply struct{}
