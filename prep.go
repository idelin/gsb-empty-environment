package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// /dev/null 在 Linux 上的设备号：主设备号 1、次设备号 3。
const (
	devNullMajor = 1
	devNullMinor = 3
)

// Ops 抽象遮蔽准备所需的系统操作，便于测试替换。
type Ops struct {
	// Lstat 不跟随符号链接地获取路径信息。
	Lstat func(path string) (os.FileInfo, error)
	// BindMount 将 source 绑定挂载到 target。
	BindMount func(source, target string) error
	// MountReadonlyTmpfs 在 target 上挂载只读空 tmpfs。
	MountReadonlyTmpfs func(target string) error
	// RemountReadonly 将 target 重挂为只读。
	RemountReadonly func(target string) error
	// DevNull 返回 /dev/null 的主、次设备号。
	DevNull func() (major, minor uint32, err error)
}

// Preparer 在容器初始化阶段执行 /proc 敏感路径遮蔽。
type Preparer struct {
	ops Ops
}

// New 返回使用默认系统操作的 Preparer（仅 Linux 可真正挂载）。
func New() *Preparer { return &Preparer{ops: defaultOps()} }

// NewWithOps 返回使用自定义操作的 Preparer，用于测试或受控环境。
func NewWithOps(ops Ops) *Preparer { return &Preparer{ops: ops} }

// Prepare 使用默认系统操作执行遮蔽准备，供容器初始化阶段调用。
func Prepare(maskedPaths, readonlyPaths []string) error {
	return New().Prepare(maskedPaths, readonlyPaths)
}

// Prepare 先遮蔽 maskedPaths，再重挂 readonlyPaths；任一步失败即返回错误。
func (p *Preparer) Prepare(maskedPaths, readonlyPaths []string) error {
	if err := p.MaskPaths(maskedPaths); err != nil {
		return err
	}
	return p.MakeReadonly(readonlyPaths)
}

// MaskPaths 逐项遮蔽：文件目标绑定 /dev/null，目录目标挂只读空 tmpfs。
// 目标不存在时跳过；路径任一级是符号链接即拒绝。
func (p *Preparer) MaskPaths(paths []string) error {
	for _, path := range paths {
		if err := p.maskPath(path); err != nil {
			return err
		}
	}
	return nil
}

// MakeReadonly 逐项重挂只读；路径存在却不能重挂视为启动失败。
func (p *Preparer) MakeReadonly(paths []string) error {
	for _, path := range paths {
		if err := p.makeReadonly(path); err != nil {
			return err
		}
	}
	return nil
}

func (p *Preparer) maskPath(path string) error {
	fi, err := resolveNoFollow(p.ops.Lstat, path)
	if err != nil {
		return err
	}
	if fi == nil {
		// 目标不存在：跳过（父级若是符号链接已在 resolveNoFollow 中拒绝）。
		return nil
	}
	if fi.IsDir() {
		if err := p.ops.MountReadonlyTmpfs(path); err != nil {
			return fmt.Errorf("遮蔽目录 %s: 挂载只读 tmpfs 失败: %w", path, err)
		}
		return nil
	}
	major, minor, err := p.ops.DevNull()
	if err != nil {
		return fmt.Errorf("遮蔽文件 %s: 校验 /dev/null 失败: %w", path, err)
	}
	if major != devNullMajor || minor != devNullMinor {
		return fmt.Errorf("遮蔽文件 %s: /dev/null 设备号 %d:%d 不符，期望 %d:%d",
			path, major, minor, devNullMajor, devNullMinor)
	}
	if err := p.ops.BindMount("/dev/null", path); err != nil {
		return fmt.Errorf("遮蔽文件 %s: 绑定 /dev/null 失败: %w", path, err)
	}
	return nil
}

func (p *Preparer) makeReadonly(path string) error {
	fi, err := resolveNoFollow(p.ops.Lstat, path)
	if err != nil {
		return err
	}
	if fi == nil {
		return nil
	}
	if err := p.ops.RemountReadonly(path); err != nil {
		return fmt.Errorf("重挂只读 %s 失败，视为启动失败: %w", path, err)
	}
	return nil
}

// resolveNoFollow 逐级以不跟随符号链接的方式解析 path：
// 任一级是符号链接即拒绝；目标不存在时返回 (nil, nil)，
// 但不存在的目标若其父级是符号链接，仍会在逐级检查中被拒绝。
func resolveNoFollow(lstat func(string) (os.FileInfo, error), path string) (os.FileInfo, error) {
	clean := filepath.Clean(path)
	cur := ""
	if filepath.IsAbs(clean) {
		cur = string(filepath.Separator)
	}
	var last os.FileInfo
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("解析 %s: 检查 %s 失败: %w", path, cur, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("解析 %s: %s 是符号链接，拒绝遮蔽", path, cur)
		}
		last = fi
	}
	if last != nil {
		return last, nil
	}
	// 路径无分量（如 "/"）：直接检查整路径。
	fi, err := lstat(clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("解析 %s: 路径是符号链接，拒绝遮蔽", path)
	}
	return fi, nil
}
