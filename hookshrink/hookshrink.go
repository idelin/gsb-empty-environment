// Package hookshrink 收缩不受信 Helm chart 的 hook 资源权限。
//
// 输入为渲染后的对象列表,按 helm.sh/hook 注解识别 hook(注解按逗号
// 拆分并去空白);约束只作用于 hook:限 release 命名空间,禁
// ClusterRole、ClusterRoleBinding、hostPath 卷与特权容器,引用的
// ServiceAccount 须已登记。
package hookshrink

import (
	"fmt"
	"strings"
)

// HookAnnotation 是 Helm 声明 hook 的注解键。
const HookAnnotation = "helm.sh/hook"

// Object 是渲染后的单个 Kubernetes 清单对象。
type Object map[string]any

// Policy 描述 hook 收缩策略。
type Policy struct {
	// ReleaseNamespace 为本次 release 的目标命名空间,hook 必须位于其中。
	ReleaseNamespace string
	// RegisteredServiceAccounts 为已登记的 ServiceAccount 名称集合。
	RegisteredServiceAccounts map[string]bool
}

// Violation 描述一个被拒绝的 hook 及其原因。
type Violation struct {
	Kind      string
	Namespace string
	Name      string
	Reason    string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s %s/%s: %s", v.Kind, v.Namespace, v.Name, v.Reason)
}

// HookTypes 解析 helm.sh/hook 注解,按逗号拆分并去除空白,忽略空项。
// 非 hook 对象返回 nil。
func HookTypes(obj Object) []string {
	raw, _ := nestedString(obj, "metadata", "annotations", HookAnnotation)
	if raw == "" {
		return nil
	}
	var types []string
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			types = append(types, t)
		}
	}
	return types
}

// IsHook 报告对象是否声明了至少一个 hook 类型。
func IsHook(obj Object) bool {
	return len(HookTypes(obj)) > 0
}

// Validate 校验单个 hook 对象,返回全部违规原因;非 hook 对象返回 nil。
func Validate(obj Object, pol Policy) []string {
	if !IsHook(obj) {
		return nil
	}
	var reasons []string

	kind, _ := obj["kind"].(string)
	switch kind {
	case "ClusterRole", "ClusterRoleBinding":
		reasons = append(reasons, "hook 禁止使用集群级资源 "+kind)
	}

	ns, _ := nestedString(obj, "metadata", "namespace")
	if ns != pol.ReleaseNamespace {
		reasons = append(reasons,
			fmt.Sprintf("hook 命名空间 %q 不在 release 命名空间 %q 内", ns, pol.ReleaseNamespace))
	}

	if hasKey(obj, "hostPath") {
		reasons = append(reasons, "hook 禁止使用 hostPath 卷")
	}
	if hasPrivileged(obj) {
		reasons = append(reasons, "hook 禁止使用特权容器")
	}
	for _, sa := range serviceAccountNames(obj) {
		if !pol.RegisteredServiceAccounts[sa] {
			reasons = append(reasons, fmt.Sprintf("ServiceAccount %q 未登记", sa))
		}
	}
	return reasons
}

// Shrink 过滤渲染后的对象列表:非 hook 对象全部保留;hook 对象逐条校验,
// 合规的保留,违规的剔除并记录 Violation。
func Shrink(objs []Object, pol Policy) (kept []Object, violations []Violation) {
	for _, obj := range objs {
		if !IsHook(obj) {
			kept = append(kept, obj)
			continue
		}
		reasons := Validate(obj, pol)
		if len(reasons) == 0 {
			kept = append(kept, obj)
			continue
		}
		kind, _ := obj["kind"].(string)
		ns, _ := nestedString(obj, "metadata", "namespace")
		name, _ := nestedString(obj, "metadata", "name")
		for _, r := range reasons {
			violations = append(violations, Violation{
				Kind: kind, Namespace: ns, Name: name, Reason: r,
			})
		}
	}
	return kept, violations
}

func nestedString(obj map[string]any, path ...string) (string, bool) {
	cur := obj
	for i, key := range path {
		v, ok := cur[key]
		if !ok {
			return "", false
		}
		if i == len(path)-1 {
			s, ok := v.(string)
			return s, ok
		}
		cur, ok = v.(map[string]any)
		if !ok {
			return "", false
		}
	}
	return "", false
}

// walk 深度优先遍历对象中的所有 map 节点。
func walk(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case Object:
		walk(map[string]any(t), fn)
	case map[string]any:
		fn(t)
		for _, child := range t {
			walk(child, fn)
		}
	case []any:
		for _, child := range t {
			walk(child, fn)
		}
	}
}

// hasKey 报告对象任意层级是否出现指定键(用于检测 hostPath)。
func hasKey(obj Object, key string) bool {
	found := false
	walk(obj, func(m map[string]any) {
		if _, ok := m[key]; ok {
			found = true
		}
	})
	return found
}

// hasPrivileged 报告任意容器 securityContext 是否启用 privileged。
func hasPrivileged(obj Object) bool {
	found := false
	walk(obj, func(m map[string]any) {
		if sc, ok := m["securityContext"].(map[string]any); ok {
			if p, ok := sc["privileged"].(bool); ok && p {
				found = true
			}
		}
	})
	return found
}

// serviceAccountNames 收集对象中引用的全部 ServiceAccount 名称。
func serviceAccountNames(obj Object) []string {
	var names []string
	walk(obj, func(m map[string]any) {
		if sa, ok := m["serviceAccountName"].(string); ok && sa != "" {
			names = append(names, sa)
		}
	})
	return names
}
