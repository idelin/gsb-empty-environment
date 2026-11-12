package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Config 描述一个双层随机化 Reporter 的完整参数。
type Config struct {
	// DeviceID 绑定永久记忆的设备标识。
	DeviceID string
	// Field 绑定永久记忆的敏感字段标识。
	Field string
	// ParamVersion 是参数版本；版本换代后会为同一字段生成新的永久记忆。
	ParamVersion string
	// Permanent 是第一层永久随机化矩阵（PRR），每（设备, 字段, 版本）只抽取一次。
	Permanent Matrix
	// Instantaneous 是第二层即时随机化矩阵（IRR），每次上报重新抽取。
	Instantaneous Matrix
	// Store 用于可靠保存永久状态。
	Store Store
	// Source 为随机源；为 nil 时使用并发安全的 math/rand/v2 全局随机源。
	Source Source
}

// Reporter 面向采集宿主，按（设备, 字段, 参数版本）维护永久随机化记忆，
// 并在每次上报时叠加即时随机化。多个 goroutine 可并发调用其方法。
type Reporter struct {
	cfg         Config
	fingerprint string
	key         string
	mu          chan struct{}
}

// NewReporter 校验参数并构建 Reporter。
func NewReporter(cfg Config) (*Reporter, error) {
	if cfg.DeviceID == "" {
		return nil, errors.New("netsec: Config.DeviceID is required")
	}
	if cfg.Field == "" {
		return nil, errors.New("netsec: Config.Field is required")
	}
	if cfg.ParamVersion == "" {
		return nil, errors.New("netsec: Config.ParamVersion is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("netsec: Config.Store is required")
	}
	if err := cfg.Permanent.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: permanent matrix invalid: %w", err)
	}
	if err := cfg.Instantaneous.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: instantaneous matrix invalid: %w", err)
	}
	if cfg.Permanent.Size() != cfg.Instantaneous.Size() {
		return nil, fmt.Errorf("%w: permanent size %d vs instantaneous size %d",
			ErrRaggedMatrix, cfg.Permanent.Size(), cfg.Instantaneous.Size())
	}
	fp, err := Fingerprint(cfg.ParamVersion, cfg.Permanent, cfg.Instantaneous)
	if err != nil {
		return nil, err
	}
	if cfg.Source == nil {
		cfg.Source = defaultSource{}
	}
	return &Reporter{
		cfg:         cfg,
		fingerprint: fp,
		key:         Key(cfg.DeviceID, cfg.Field, cfg.ParamVersion),
		mu:          make(chan struct{}, 1),
	}, nil
}

// Key 返回该 Reporter 永久记录使用的存储键。
func (r *Reporter) Key() string { return r.key }

// Fingerprint 返回该 Reporter 的参数指纹。
func (r *Reporter) Fingerprint() string { return r.fingerprint }

// Permanent 返回（必要时创建并持久化）永久随机化符号。
//
// 语义保证：同一（设备, 字段, 参数版本）的 PRR 抽取至多发生一次。存储中
// 已有时直接复用；首次并发调用通过每键串行化保证只有一个调用者执行抽取与
// 提交，提交失败不落任何内存缓存，后续重试会重新走 Load→抽取→Commit 流程。
func (r *Reporter) Permanent(ctx context.Context, truth int) (int, error) {
	if truth < 0 || truth >= r.cfg.Permanent.Size() {
		return 0, fmt.Errorf("%w: truth %d", ErrSymbolOutOfRange, truth)
	}
	if rec, err := r.cfg.Store.Load(ctx, r.key); err == nil {
		return r.verify(rec)
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}

	r.lock()
	defer r.unlock()

	// 双检：等待锁期间可能已有其他调用者完成了首次提交。
	rec, err := r.cfg.Store.Load(ctx, r.key)
	if err == nil {
		return r.verify(rec)
	}
	if !errors.Is(err, ErrNotFound) {
		return 0, err
	}

	permanent, err := drawMatrix(r.cfg.Permanent, truth, r.cfg.Source)
	if err != nil {
		return 0, err
	}
	fresh := Record{
		DeviceID:     r.cfg.DeviceID,
		Field:        r.cfg.Field,
		ParamVersion: r.cfg.ParamVersion,
		Fingerprint:  r.fingerprint,
		Truth:        truth,
		Permanent:    permanent,
	}
	// Commit 成功才算抽取完成；失败时不缓存，调用者重试会重新抽取，
	// 但永远不会有第二个结果被持久化。
	if err := r.cfg.Store.Commit(ctx, r.key, fresh); err != nil {
		return 0, err
	}
	return permanent, nil
}

// Report 执行一次完整上报：确保永久记忆存在，再按即时矩阵从永久状态抽取
// 本次上报值。IRR 每次调用都重新抽取，不落盘、不记忆。
func (r *Reporter) Report(ctx context.Context, truth int) (int, error) {
	permanent, err := r.Permanent(ctx, truth)
	if err != nil {
		return 0, err
	}
	return drawMatrix(r.cfg.Instantaneous, permanent, r.cfg.Source)
}

func (r *Reporter) verify(rec Record) (int, error) {
	if rec.Fingerprint != r.fingerprint {
		return 0, &FingerprintMismatchError{Key: r.key, Got: rec.Fingerprint, Expected: r.fingerprint}
	}
	return rec.Permanent, nil
}

func (r *Reporter) lock()   { r.mu <- struct{}{} }
func (r *Reporter) unlock() { <-r.mu }
