//go:build linux

package procid

import (
	"fmt"
	"os"
)

// LinuxProcFS 基于真实 procfs 的 ProcFS 实现，root 一般为 "/proc"。
type LinuxProcFS struct {
	root string
}

// DefaultProcFS 返回挂载在标准位置 /proc 的生产实现。
func DefaultProcFS() *LinuxProcFS {
	return &LinuxProcFS{root: "/proc"}
}

// NewLinuxProcFS 允许自定义 procfs 挂载点（主要用于测试/容器场景）。
func NewLinuxProcFS(root string) *LinuxProcFS {
	return &LinuxProcFS{root: root}
}

// ReadStat 读取 /proc/<pid>/stat 的原始文本。
func (p *LinuxProcFS) ReadStat(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", p.root, pid))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadExe 对 /proc/<pid>/exe 做 readlink。镜像被替换时内核返回的目标
// 会带有 " (deleted)" 后缀，由 IsExeDeleted 统一判定。
func (p *LinuxProcFS) ReadExe(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("%s/%d/exe", p.root, pid))
}
