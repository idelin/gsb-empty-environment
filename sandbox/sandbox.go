// Package sandbox 提供多租户规则表达式的安全求值沙箱。
//
// 安全模型:
//   - 解析后对 AST 做静态校验,函数调用必须在白名单内,
//     严禁触达宿主环境的任何未授权函数/资源;
//   - 执行过程按"指令步数"计费(解析、校验、求值、内建调用均计费),
//     超过 MaxSteps 立即以 ErrBudgetExceeded 中断;
//   - 字符串/调用帧等内存申请计入 MaxMemoryBytes,超额以 ErrMemoryExceeded 中断;
//   - 支持 Timeout 与 context 取消,超时以 ErrTimeout 中断。
package sandbox

import (
	"context"
	"fmt"
	"time"
)

// Config 沙箱配额配置。零值字段取默认值。
type Config struct {
	MaxSteps       int             // 最大指令步数预算(默认 1000)
	MaxMemoryBytes int64           // 最大内存申请量,字节(默认 1 MiB)
	MaxASTDepth    int             // AST 最大深度(默认 128)
	MaxNodes       int             // AST 最大节点数(默认 512)
	MaxExprLen     int             // 表达式最大长度,字节(默认 4096)
	MaxEvalDepth   int             // eval 最大递归深度(默认 512)
	Timeout        time.Duration   // 单次执行超时(默认 5s)
	AllowedFuncs   map[string]bool // 函数白名单(nil 用默认白名单)
}

// DefaultAllowedFuncs 默认白名单:全部为纯函数。
func DefaultAllowedFuncs() map[string]bool {
	return map[string]bool{
		"abs": true, "floor": true, "ceil": true, "round": true,
		"min": true, "max": true,
		"len": true, "concat": true,
		"eval": true, // 沙箱内自求值,共享预算,无法逃逸
	}
}

func (c Config) withDefaults() Config {
	if c.MaxSteps <= 0 {
		c.MaxSteps = 1000
	}
	if c.MaxMemoryBytes <= 0 {
		c.MaxMemoryBytes = 1 << 20
	}
	if c.MaxASTDepth <= 0 {
		c.MaxASTDepth = 128
	}
	if c.MaxNodes <= 0 {
		c.MaxNodes = 512
	}
	if c.MaxExprLen <= 0 {
		c.MaxExprLen = 4096
	}
	if c.MaxEvalDepth <= 0 {
		c.MaxEvalDepth = 512
	}
	if c.Timeout <= 0 {
		c.Timeout = 5 * time.Second
	}
	if c.AllowedFuncs == nil {
		c.AllowedFuncs = DefaultAllowedFuncs()
	}
	return c
}

// Sandbox 多租户规则沙箱。一个 Sandbox 可并发执行多条表达式,
// 每次执行拥有独立的预算计量器。
type Sandbox struct {
	cfg Config
}

// New 创建沙箱。
func New(cfg Config) *Sandbox {
	return &Sandbox{cfg: cfg.withDefaults()}
}

// Run 在沙箱中解析、校验并执行表达式,返回求值结果
// (float64 / string / bool)。env 为租户注入的变量。
func (s *Sandbox) Run(ctx context.Context, expr string, env map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(expr) > s.cfg.MaxExprLen {
		return nil, fmt.Errorf("%w: expression length %d exceeds limit %d", ErrValidation, len(expr), s.cfg.MaxExprLen)
	}
	if env == nil {
		env = map[string]any{}
	}
	ev := &evaluator{
		cfg:      s.cfg,
		env:      env,
		ctx:      ctx,
		deadline: time.Now().Add(s.cfg.Timeout),
		sb:       s,
	}
	node, err := parse(expr, ev.charge)
	if err != nil {
		return nil, err
	}
	if err := s.validate(node, ev); err != nil {
		return nil, err
	}
	return ev.eval(node)
}
