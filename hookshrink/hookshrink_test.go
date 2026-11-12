package hookshrink

import (
	"reflect"
	"strings"
	"testing"
)

func meta(namespace, name string, annotations map[string]any) map[string]any {
	m := map[string]any{"namespace": namespace, "name": name}
	if annotations != nil {
		m["annotations"] = annotations
	}
	return m
}

func hookJob(namespace, name, hookValue string, podSpec map[string]any) Object {
	return Object{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   meta(namespace, name, map[string]any{HookAnnotation: hookValue}),
		"spec": map[string]any{
			"template": map[string]any{
				"spec": podSpec,
			},
		},
	}
}

func TestHookTypesMultiValue(t *testing.T) {
	obj := Object{
		"kind": "Job",
		"metadata": meta("rel", "migrate", map[string]any{
			HookAnnotation: " pre-install,pre-upgrade ,, post-rollback ",
		}),
	}
	got := HookTypes(obj)
	want := []string{"pre-install", "pre-upgrade", "post-rollback"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HookTypes = %v, want %v", got, want)
	}
}

func TestHookTypesAbsent(t *testing.T) {
	obj := Object{"kind": "Deployment", "metadata": meta("rel", "web", nil)}
	if types := HookTypes(obj); types != nil {
		t.Fatalf("HookTypes = %v, want nil", types)
	}
	if IsHook(obj) {
		t.Fatal("IsHook = true, want false")
	}
}

func TestShrinkCrossNamespaceHookRejected(t *testing.T) {
	pol := Policy{ReleaseNamespace: "rel"}
	objs := []Object{
		hookJob("other", "sneaky", "pre-install", map[string]any{
			"containers": []any{map[string]any{"name": "c", "image": "x"}},
		}),
	}
	kept, violations := Shrink(objs, pol)
	if len(kept) != 0 {
		t.Fatalf("kept %d objects, want 0", len(kept))
	}
	if len(violations) != 1 || !strings.Contains(violations[0].Reason, "命名空间") {
		t.Fatalf("violations = %v, want one namespace violation", violations)
	}
}

func TestShrinkClusterScopedKindsRejected(t *testing.T) {
	pol := Policy{ReleaseNamespace: "rel"}
	for _, kind := range []string{"ClusterRole", "ClusterRoleBinding"} {
		obj := Object{
			"kind":     kind,
			"metadata": meta("", "escalate", map[string]any{HookAnnotation: "pre-install"}),
		}
		kept, violations := Shrink([]Object{obj}, pol)
		if len(kept) != 0 {
			t.Fatalf("%s: kept %d objects, want 0", kind, len(kept))
		}
		if len(violations) == 0 {
			t.Fatalf("%s: no violations recorded", kind)
		}
	}
}

func TestShrinkHostPathAndPrivilegedRejected(t *testing.T) {
	pol := Policy{ReleaseNamespace: "rel"}
	hostPathJob := hookJob("rel", "hostpath", "pre-install", map[string]any{
		"volumes": []any{map[string]any{
			"name":     "dockersock",
			"hostPath": map[string]any{"path": "/var/run/docker.sock"},
		}},
		"containers": []any{map[string]any{"name": "c", "image": "x"}},
	})
	privJob := hookJob("rel", "priv", "pre-install", map[string]any{
		"containers": []any{map[string]any{
			"name":            "c",
			"image":           "x",
			"securityContext": map[string]any{"privileged": true},
		}},
	})
	kept, violations := Shrink([]Object{hostPathJob, privJob}, pol)
	if len(kept) != 0 {
		t.Fatalf("kept %d objects, want 0", len(kept))
	}
	var hostPathHit, privHit bool
	for _, v := range violations {
		hostPathHit = hostPathHit || strings.Contains(v.Reason, "hostPath")
		privHit = privHit || strings.Contains(v.Reason, "特权容器")
	}
	if !hostPathHit || !privHit {
		t.Fatalf("violations = %v, want hostPath and privileged hits", violations)
	}
}

func TestShrinkServiceAccountRegistration(t *testing.T) {
	pol := Policy{
		ReleaseNamespace:          "rel",
		RegisteredServiceAccounts: map[string]bool{"builder": true},
	}
	unregistered := hookJob("rel", "rogue", "pre-install", map[string]any{
		"serviceAccountName": "cluster-admin-sa",
		"containers":         []any{map[string]any{"name": "c", "image": "x"}},
	})
	registered := hookJob("rel", "ok", "pre-install", map[string]any{
		"serviceAccountName": "builder",
		"containers":         []any{map[string]any{"name": "c", "image": "x"}},
	})
	kept, violations := Shrink([]Object{unregistered, registered}, pol)
	if len(kept) != 1 {
		t.Fatalf("kept %d objects, want 1", len(kept))
	}
	if len(violations) != 1 || !strings.Contains(violations[0].Reason, "未登记") {
		t.Fatalf("violations = %v, want one unregistered SA violation", violations)
	}
}

func TestShrinkKeepsNonHookAndCompliantHook(t *testing.T) {
	pol := Policy{ReleaseNamespace: "rel"}
	plain := Object{"kind": "Deployment", "metadata": meta("rel", "web", nil)}
	compliant := hookJob("rel", "migrate", "pre-install, pre-upgrade", map[string]any{
		"containers": []any{map[string]any{"name": "c", "image": "x"}},
	})
	kept, violations := Shrink([]Object{plain, compliant}, pol)
	if len(violations) != 0 {
		t.Fatalf("violations = %v, want none", violations)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d objects, want 2", len(kept))
	}
}
