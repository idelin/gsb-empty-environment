package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
)

// ErrNotExist 由 FileSystem.Stat 在目标不存在时返回。
var ErrNotExist = errors.New("netsec: resource does not exist")

// 方法常量。
const (
	MethodCopy = "COPY"
	MethodMove = "MOVE"
)

// Request 描述一次 COPY 或 MOVE 操作，字段取自 HTTP 请求头。
type Request struct {
	Method      string // MethodCopy 或 MethodMove
	Host        string // 请求 Host 头
	Source      string // 源 URL 路径（允许百分号编码）
	Destination string // Destination 头（绝对 URI 或绝对路径）
	Depth       string // "0" 或 "infinity"；空按 "infinity" 处理
	Overwrite   bool   // Overwrite 头解析结果（"T" 为 true）
}

// ItemStatus 是 207 多状态中的单项结果，Href 为源侧路径。
type ItemStatus struct {
	Href   string
	Status int
	Err    string
}

// Result 为引擎输出，由处理器映射为 HTTP 响应。
type Result struct {
	// Status 为整体状态码：201/204（成功）、207（部分失败且保留）、
	// 412（Overwrite: F 且目标已存在）、403（整体拒绝或已回滚）、400。
	Status int
	// Items 逐项结果（含失败项的 403/500），供渲染 207 多状态。
	Items []ItemStatus
	// Copied 为实际保留的已复制目标路径。
	Copied []string
	// RolledBack 表示已按 RollbackAll 策略撤销全部复制。
	RolledBack bool
	// Dest 为规范化后的目标路径。
	Dest string
}

// Engine 为 WebDAV 处理器提供 COPY/MOVE 的授权与执行编排。
type Engine struct {
	Auth Authorizer
	FS   FileSystem
	Opts Options
}

// NewEngine 构造引擎；tenantRoot 规范化后存入配置。
func NewEngine(auth Authorizer, fs FileSystem, opts Options) (*Engine, error) {
	root, err := CanonicalTenantPath(opts.TenantRoot, "/")
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid TenantRoot: %w", err)
	}
	opts.TenantRoot = root
	return &Engine{Auth: auth, FS: fs, Opts: opts}, nil
}

// CopyOrMove 校验请求、逐项授权并按配置执行复制/移动。
func (e *Engine) CopyOrMove(ctx context.Context, req Request) (*Result, error) {
	if req.Method != MethodCopy && req.Method != MethodMove {
		return nil, fmt.Errorf("netsec: unsupported method %q", req.Method)
	}
	depth := req.Depth
	if depth == "" {
		depth = "infinity"
	}
	if depth != "0" && depth != "infinity" {
		return &Result{Status: http.StatusBadRequest}, nil
	}

	src, err := CanonicalTenantPath(req.Source, e.Opts.TenantRoot)
	if err != nil {
		return &Result{Status: http.StatusBadRequest}, nil
	}
	dest, err := ValidateDestination(req.Destination, req.Host, e.Opts.TenantRoot)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrCrossHost) || errors.Is(err, ErrCrossTenant) {
			status = http.StatusForbidden
		}
		return &Result{Status: status, Dest: dest}, nil
	}
	res := &Result{Dest: dest}

	// 不允许把集合复制到自身或其子孙内。
	if dest == src || strings.HasPrefix(dest, src+"/") {
		res.Status = http.StatusForbidden
		return res, nil
	}

	// 目标存在性与 Overwrite: F 语义（RFC 4918 9.8.5：返回 412）。
	_, statErr := e.FS.Stat(ctx, dest)
	destExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, ErrNotExist) {
		return nil, statErr
	}
	if destExists && !req.Overwrite {
		res.Status = http.StatusPreconditionFailed
		return res, nil
	}

	// 目标集合（父级）写权限；覆盖已存在目标时还需目标自身写权限。
	parent := path.Dir(dest)
	if !e.Auth.CanWrite(ctx, e.Opts.Tenant, parent) {
		res.Status = http.StatusForbidden
		res.Items = []ItemStatus{{Href: dest, Status: http.StatusForbidden, Err: "destination collection not writable"}}
		return res, nil
	}
	if destExists && !e.Auth.CanWrite(ctx, e.Opts.Tenant, dest) {
		res.Status = http.StatusForbidden
		res.Items = []ItemStatus{{Href: dest, Status: http.StatusForbidden, Err: "overwrite target not writable"}}
		return res, nil
	}

	// 枚举源成员。
	var members []Member
	if depth == "0" {
		root, err := e.FS.Stat(ctx, src)
		if err != nil {
			return nil, err
		}
		members = []Member{root}
	} else {
		var err error
		members, err = e.FS.Walk(ctx, src)
		if err != nil {
			return nil, err
		}
	}
	if len(members) == 0 || members[0].Path != src {
		return nil, fmt.Errorf("netsec: Walk must return source root first")
	}

	// 逐项授权：读权限（MOVE 另需删除权限）、符号链接策略。
	type op struct {
		m Member
		d string
	}
	var plan []op
	var failures []ItemStatus
	denied := map[string]bool{} // 被拒集合前缀，后代一并跳过
	for _, m := range members {
		if m.Path != src && underDenied(m.Path, denied) {
			continue
		}
		deny := func(status int, why string) {
			failures = append(failures, ItemStatus{Href: m.Path, Status: status, Err: why})
			if m.Collection {
				denied[m.Path] = true
			}
		}
		if m.Symlink && !e.Opts.AllowSymlinks {
			deny(http.StatusForbidden, "symlink members are not copied")
			continue
		}
		if !e.Auth.CanRead(ctx, e.Opts.Tenant, m.Path) {
			deny(http.StatusForbidden, "read denied")
			continue
		}
		if req.Method == MethodMove && !e.Auth.CanDelete(ctx, e.Opts.Tenant, m.Path) {
			deny(http.StatusForbidden, "delete denied on move source")
			continue
		}
		rel := strings.TrimPrefix(m.Path, src)
		plan = append(plan, op{m: m, d: dest + rel})
	}

	// 根成员被拒：整体失败，无可执行计划。
	if len(failures) > 0 && failures[0].Href == src {
		res.Status = http.StatusForbidden
		res.Items = failures
		return res, nil
	}

	// 执行：覆盖时先删除已存在目标，再按父先子后顺序复制。
	var copied []string
	overwriteFailed := false
	if destExists {
		if err := e.FS.Remove(ctx, dest); err != nil {
			failures = append(failures, ItemStatus{Href: dest, Status: http.StatusInternalServerError, Err: "overwrite remove failed"})
			overwriteFailed = true
		}
	}
	if !overwriteFailed {
		failedDirs := map[string]bool{}
		for _, o := range plan {
			if o.m.Path != src && underDenied(o.m.Path, failedDirs) {
				continue
			}
			if err := e.FS.Copy(ctx, o.m.Path, o.d); err != nil {
				failures = append(failures, ItemStatus{Href: o.m.Path, Status: http.StatusInternalServerError, Err: err.Error()})
				if o.m.Collection {
					failedDirs[o.m.Path] = true
				}
				continue
			}
			copied = append(copied, o.d)
		}
	}

	if len(failures) > 0 {
		res.Items = failures
		if e.Opts.OnPartialFailure == RollbackAll {
			// 逆序撤销已复制项；MOVE 不触碰源。
			for i := len(copied) - 1; i >= 0; i-- {
				_ = e.FS.Remove(ctx, copied[i])
			}
			res.RolledBack = true
			res.Status = http.StatusForbidden
			return res, nil
		}
		res.Copied = copied
		res.Status = http.StatusMultiStatus
		return res, nil
	}

	// 全部成功：MOVE 删除源树。
	if req.Method == MethodMove {
		if err := e.FS.Remove(ctx, src); err != nil {
			return nil, fmt.Errorf("netsec: remove move source: %w", err)
		}
	}
	res.Copied = copied
	if destExists {
		res.Status = http.StatusNoContent
	} else {
		res.Status = http.StatusCreated
	}
	return res, nil
}

func underDenied(p string, denied map[string]bool) bool {
	for d := range denied {
		if strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// sortedItems 返回按 Href 排序的逐项结果，便于稳定渲染。
func (r *Result) sortedItems() []ItemStatus {
	items := append([]ItemStatus(nil), r.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].Href < items[j].Href })
	return items
}
