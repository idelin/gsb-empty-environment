package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// RoleRelease 标识发布角色。pull_request 事件（尤其是 fork 仓库发起的）
// 不得换发该角色的凭据。
const RoleRelease = "release"

var (
	ErrRepositoryMismatch    = errors.New("netsec: repository identity mismatch")
	ErrSubjectMismatch       = errors.New("netsec: subject does not match binding pattern")
	ErrWorkflowRefNotAllowed = errors.New("netsec: job_workflow_ref not in allowlist")
	ErrRoleNotAllowed        = errors.New("netsec: role not allowed for this event")
)

// Claims 是调用方已完成签名校验的 OIDC claims。
// 本包不再验签，仅做授权匹配。
type Claims struct {
	RepositoryID      string // 仓库的数字 ID，改名/重建后不变才可信
	RepositoryOwnerID string // 所有者的数字 ID
	Subject           string // sub，形如 repo:org/name:ref:refs/heads/main
	JobWorkflowRef    string // job_workflow_ref，可复用工作流的引用
	EventName         string // 触发事件，如 push、pull_request
}

// Binding 是一条联邦主体绑定记录，描述允许换发某角色的主体条件。
type Binding struct {
	RepositoryID      string
	RepositoryOwnerID string
	SubjectPattern    string   // 支持 * 通配，但仅匹配完整段（按 ':' 分段）
	AllowedJobRefs    []string // job_workflow_ref 许可表；空表示不校验
	Role              string   // 匹配成功后授予的角色
}

// Match 校验 claims 是否满足绑定记录，全部通过时返回 nil。
func (b Binding) Match(c Claims) error {
	// 仓库可被改名或删除重建，名称不可信，必须比对不可伪造的数字 ID。
	if c.RepositoryID == "" || c.RepositoryID != b.RepositoryID ||
		c.RepositoryOwnerID == "" || c.RepositoryOwnerID != b.RepositoryOwnerID {
		return fmt.Errorf("%w: claims repo %s/%s, binding %s/%s",
			ErrRepositoryMismatch, c.RepositoryOwnerID, c.RepositoryID,
			b.RepositoryOwnerID, b.RepositoryID)
	}
	if !matchSubject(b.SubjectPattern, c.Subject) {
		return fmt.Errorf("%w: sub %q does not match pattern %q",
			ErrSubjectMismatch, c.Subject, b.SubjectPattern)
	}
	if len(b.AllowedJobRefs) > 0 && !contains(b.AllowedJobRefs, c.JobWorkflowRef) {
		return fmt.Errorf("%w: %q", ErrWorkflowRefNotAllowed, c.JobWorkflowRef)
	}
	// fork 的 pull_request 可携带任意仓库的 workflow 上下文，
	// 必须阻断其获取发布角色。
	if b.Role == RoleRelease && c.EventName == "pull_request" {
		return fmt.Errorf("%w: %s role cannot be issued for pull_request",
			ErrRoleNotAllowed, RoleRelease)
	}
	return nil
}

// matchSubject 按 ':' 和 '/' 分段比较；模式中的 '*' 只匹配一个完整段，
// 不做段内前缀/子串匹配，段数必须相等。
func matchSubject(pattern, subject string) bool {
	if pattern == "" || subject == "" {
		return false
	}
	pp := splitSegments(pattern)
	sp := splitSegments(subject)
	if len(pp) != len(sp) {
		return false
	}
	for i := range pp {
		if pp[i] == "*" {
			if sp[i] == "" {
				return false
			}
			continue
		}
		if pp[i] != sp[i] {
			return false
		}
	}
	return true
}

func splitSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ':' || r == '/'
	})
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
