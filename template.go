package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 安全相关保留字段:派生实例与模板更新补丁均不得触碰,
// 任何变更必须重新审批。
var securityBoundRoots = map[string]bool{
	"image":    true,
	"identity": true,
	"security": true,
}

// ImageRef 为规范化后的镜像引用,必须锁定摘要。
type ImageRef struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// Identity 为工作负载的服务身份。
type Identity struct {
	ServiceAccount string `json:"serviceAccount"`
	SPIFFEID       string `json:"spiffeID,omitempty"`
}

// SecurityOptions 为审批绑定的安全选项。指针字段用于区分
// "未设置"与"显式设置",规范化时填充默认值。
type SecurityOptions struct {
	RunAsNonRoot             *bool  `json:"runAsNonRoot"`
	ReadOnlyRootFilesystem   *bool  `json:"readOnlyRootFilesystem"`
	AllowPrivilegeEscalation *bool  `json:"allowPrivilegeEscalation"`
	SeccompProfile           string `json:"seccompProfile"`
}

// Template 为扩容控制器使用的工作负载模板。
// Revision 由 Store 维护,用于并发更新的乐观并发控制。
type Template struct {
	Revision int64           `json:"revision"`
	Image    ImageRef        `json:"image"`
	Identity Identity        `json:"identity"`
	Security SecurityOptions `json:"security"`
	Fields   map[string]any  `json:"fields,omitempty"`
}

func boolPtr(v bool) *bool { return &v }

// Normalize 返回规范化模板:镜像名小写、校验摘要锁定、
// 填充安全选项默认值、修剪身份字段。规范化是审批绑定的前提,
// 保证同一逻辑模板得到唯一的绑定摘要。
func Normalize(t Template) (Template, error) {
	t.Image.Name = strings.ToLower(strings.TrimSpace(t.Image.Name))
	if t.Image.Name == "" {
		return t, errors.New("netsec: 镜像名不能为空")
	}
	if !strings.HasPrefix(t.Image.Digest, "sha256:") || len(t.Image.Digest) != len("sha256:")+64 {
		return t, fmt.Errorf("netsec: 镜像必须锁定 sha256 摘要,得到 %q", t.Image.Digest)
	}
	t.Identity.ServiceAccount = strings.TrimSpace(t.Identity.ServiceAccount)
	t.Identity.SPIFFEID = strings.TrimSpace(t.Identity.SPIFFEID)
	if t.Identity.ServiceAccount == "" {
		return t, errors.New("netsec: 服务身份 ServiceAccount 不能为空")
	}
	if t.Security.RunAsNonRoot == nil {
		t.Security.RunAsNonRoot = boolPtr(true)
	}
	if t.Security.ReadOnlyRootFilesystem == nil {
		t.Security.ReadOnlyRootFilesystem = boolPtr(true)
	}
	if t.Security.AllowPrivilegeEscalation == nil {
		t.Security.AllowPrivilegeEscalation = boolPtr(false)
	}
	if t.Security.SeccompProfile == "" {
		t.Security.SeccompProfile = "RuntimeDefault"
	}
	if t.Fields == nil {
		t.Fields = map[string]any{}
	}
	return t, nil
}

// BindingDigest 计算审批绑定摘要:仅覆盖规范化后的
// 镜像、身份与安全选项。摘要不匹配即表示审批后模板被变更。
func BindingDigest(t Template) string {
	canon := struct {
		Image    ImageRef        `json:"image"`
		Identity Identity        `json:"identity"`
		Security SecurityOptions `json:"security"`
	}{t.Image, t.Identity, t.Security}
	raw, _ := json.Marshal(canon)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
