package netsec

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Mapping 表示 /proc/PID/maps 中的一条 VMA 记录。
type Mapping struct {
	Start    uint64 // VMA 起始虚拟地址
	End      uint64 // VMA 结束虚拟地址（不含）
	Perms    string // 形如 "rw-s" 的权限串
	Offset   uint64 // 映射起始处对应的后备文件偏移
	DevMajor uint64
	DevMinor uint64
	Inode    uint64
	Path     string // 原始路径，可能含空格或 "(deleted)"，匿名映射为空
}

// Shared 报告该映射是否为共享映射（私有写时复制映射不算别名）。
func (m Mapping) Shared() bool { return len(m.Perms) >= 4 && m.Perms[3] == 's' }

// Writable 报告该映射是否可写。
func (m Mapping) Writable() bool { return len(m.Perms) >= 2 && m.Perms[1] == 'w' }

// Executable 报告该映射是否可执行。
func (m Mapping) Executable() bool { return len(m.Perms) >= 3 && m.Perms[2] == 'x' }

// FileStart 返回该映射在后备对象上的起始文件偏移。
func (m Mapping) FileStart() uint64 { return m.Offset }

// FileEnd 返回该映射在后备对象上的结束文件偏移（不含）。
func (m Mapping) FileEnd() uint64 { return m.Offset + (m.End - m.Start) }

// Alias 表示同一后备对象上一对可写/可执行共享映射的重叠。
// OverlapStart/OverlapEnd 是后备对象上的文件偏移区间（End 不含）。
type Alias struct {
	Device       string // "major:minor"
	Inode        uint64
	Path         string
	Writable     Mapping
	Executable   Mapping
	OverlapStart uint64
	OverlapEnd   uint64
}

// ParseMaps 解析 /proc/PID/maps 文本。路径字段按原始剩余内容保留，
// 因此 memfd 名称中的空格和 "(deleted)" 后缀都能完整识别。
func ParseMaps(r io.Reader) ([]Mapping, error) {
	var out []Mapping
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		m, err := parseMapsLine(line)
		if err != nil {
			return nil, fmt.Errorf("maps 第 %d 行: %w", lineNo, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseMapsLine(line string) (Mapping, error) {
	var m Mapping
	rest := line
	next := func() (string, error) {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return "", fmt.Errorf("字段不足: %q", line)
		}
		if i := strings.IndexAny(rest, " \t"); i >= 0 {
			tok := rest[:i]
			rest = rest[i:]
			return tok, nil
		}
		tok := rest
		rest = ""
		return tok, nil
	}

	addr, err := next()
	if err != nil {
		return m, err
	}
	dash := strings.IndexByte(addr, '-')
	if dash <= 0 {
		return m, fmt.Errorf("地址区间格式错误: %q", addr)
	}
	if m.Start, err = strconv.ParseUint(addr[:dash], 16, 64); err != nil {
		return m, fmt.Errorf("起始地址无效: %w", err)
	}
	if m.End, err = strconv.ParseUint(addr[dash+1:], 16, 64); err != nil {
		return m, fmt.Errorf("结束地址无效: %w", err)
	}
	if m.End <= m.Start {
		return m, fmt.Errorf("地址区间为空: %q", addr)
	}

	if m.Perms, err = next(); err != nil {
		return m, err
	}
	if len(m.Perms) != 4 {
		return m, fmt.Errorf("权限串格式错误: %q", m.Perms)
	}

	var tok string
	if tok, err = next(); err != nil {
		return m, err
	}
	if m.Offset, err = strconv.ParseUint(tok, 16, 64); err != nil {
		return m, fmt.Errorf("文件偏移无效: %w", err)
	}

	if tok, err = next(); err != nil {
		return m, err
	}
	colon := strings.IndexByte(tok, ':')
	if colon <= 0 {
		return m, fmt.Errorf("设备号格式错误: %q", tok)
	}
	if m.DevMajor, err = strconv.ParseUint(tok[:colon], 16, 64); err != nil {
		return m, fmt.Errorf("主设备号无效: %w", err)
	}
	if m.DevMinor, err = strconv.ParseUint(tok[colon+1:], 16, 64); err != nil {
		return m, fmt.Errorf("次设备号无效: %w", err)
	}

	if tok, err = next(); err != nil {
		return m, err
	}
	if m.Inode, err = strconv.ParseUint(tok, 10, 64); err != nil {
		return m, fmt.Errorf("inode 无效: %w", err)
	}

	// 路径是行内剩余部分，原样保留（可能含空格、"(deleted)"，也可能为空）。
	m.Path = strings.TrimLeft(rest, " \t")
	return m, nil
}

// fileKey 标识一个后备对象：设备号 + inode。
type fileKey struct {
	major uint64
	minor uint64
	inode uint64
}

// FindWritableExecAliases 在 maps 中找出同一后备对象（设备 + inode）上
// 文件偏移区间互相重叠、且一个可写另一个可执行的共享映射对。
// 私有（写时复制）映射与匿名映射（inode 为 0）不参与判定。
func FindWritableExecAliases(maps []Mapping) []Alias {
	groups := make(map[fileKey][]Mapping)
	for _, m := range maps {
		if m.Inode == 0 || !m.Shared() {
			continue
		}
		if !m.Writable() && !m.Executable() {
			continue
		}
		k := fileKey{m.DevMajor, m.DevMinor, m.Inode}
		groups[k] = append(groups[k], m)
	}

	var out []Alias
	for k, g := range groups {
		for i := 0; i < len(g); i++ {
			for j := i + 1; j < len(g); j++ {
				a, b := g[i], g[j]
				var w, x Mapping
				switch {
				case a.Writable() && b.Executable():
					w, x = a, b
				case b.Writable() && a.Executable():
					w, x = b, a
				default:
					continue
				}
				lo := max(w.FileStart(), x.FileStart())
				hi := min(w.FileEnd(), x.FileEnd())
				if lo >= hi {
					continue
				}
				path := w.Path
				if path == "" {
					path = x.Path
				}
				out = append(out, Alias{
					Device:       fmt.Sprintf("%02x:%02x", k.major, k.minor),
					Inode:        k.inode,
					Path:         path,
					Writable:     w,
					Executable:   x,
					OverlapStart: lo,
					OverlapEnd:   hi,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Device != out[j].Device {
			return out[i].Device < out[j].Device
		}
		if out[i].Inode != out[j].Inode {
			return out[i].Inode < out[j].Inode
		}
		return out[i].OverlapStart < out[j].OverlapStart
	})
	return out
}

// FindAliases 解析 maps 文本并返回可写/可执行别名，供主机防护服务直接调用。
func FindAliases(r io.Reader) ([]Alias, error) {
	maps, err := ParseMaps(r)
	if err != nil {
		return nil, err
	}
	return FindWritableExecAliases(maps), nil
}
