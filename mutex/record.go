package mutex

// Record 是持久化在 <name>.lock.json 中的锁所有者记录。
type Record struct {
	// Token 是单调递增的隔离令牌（fencing token）。
	// 每次成功获取（含接管）都使其 +1，释放时保留不清零，
	// 因此对同一把锁它在其生命周期内严格递增。
	Token uint64 `json:"token"`
	// OwnerID 是持有句柄的随机 ID（每次 New 生成），
	// 用于释放/续约时校验身份，防止误操作他人的锁。
	OwnerID string `json:"owner_id,omitempty"`
	// PID 与 StartToken 共同标识持有者进程实例：
	// PID 复用后 StartToken 不再匹配，记录被判为陈旧。
	PID        int    `json:"pid,omitempty"`
	Host       string `json:"host,omitempty"`
	StartToken string `json:"start_token,omitempty"`
	// ExpiresAt 是租约到期时刻（UnixNano，由注入的 Clock 度量）。
	ExpiresAt int64 `json:"expires_at_unix_nano,omitempty"`
}

// Free 表示记录为"已释放"占位（保留 Token 以维持单调性）。
func (r Record) Free() bool { return r.OwnerID == "" }
