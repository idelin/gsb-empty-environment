package netsec

import (
	"errors"
	"testing"
)

func newTestAuthorizer() *Authorizer {
	return NewAuthorizer([]OperationSpec{
		{
			Namespace: "billing",
			Name:      "CreateInvoice",
			Version:   "v1",
			Roles:     []string{"billing:write", "admin"},
			Scopes:    []string{"invoice"},
			Extensions: map[string]string{
				"trace-id": "",
				"mode":     "sync",
			},
		},
		{
			Namespace: "billing",
			Name:      "GetInvoice",
			Version:   "v1",
			Roles:     []string{"billing:read", "admin"},
			Scopes:    []string{"invoice"},
		},
	})
}

func TestUndeclaredOperationDenied(t *testing.T) {
	auth := newTestAuthorizer()

	cases := []Request{
		// 未声明的操作名
		{Namespace: "billing", Operation: "DeleteInvoice", Version: "v1", Roles: []string{"admin"}, Scope: "invoice"},
		// 未声明的命名空间
		{Namespace: "iam", Operation: "CreateInvoice", Version: "v1", Roles: []string{"admin"}, Scope: "invoice"},
		// 未声明的版本
		{Namespace: "billing", Operation: "CreateInvoice", Version: "v2", Roles: []string{"admin"}, Scope: "invoice"},
	}
	for _, req := range cases {
		if err := auth.Authorize(req); !errors.Is(err, ErrOperationNotDeclared) {
			t.Errorf("Authorize(%+v) = %v, want ErrOperationNotDeclared", req, err)
		}
	}
}

func TestRoleEscalationDenied(t *testing.T) {
	auth := newTestAuthorizer()

	cases := []Request{
		// 只读角色调用写操作
		{Namespace: "billing", Operation: "CreateInvoice", Version: "v1", Roles: []string{"billing:read"}, Scope: "invoice"},
		// 无任何角色
		{Namespace: "billing", Operation: "GetInvoice", Version: "v1", Scope: "invoice"},
		// 无关角色
		{Namespace: "billing", Operation: "GetInvoice", Version: "v1", Roles: []string{"inventory:read"}, Scope: "invoice"},
	}
	for _, req := range cases {
		if err := auth.Authorize(req); !errors.Is(err, ErrRoleNotGranted) {
			t.Errorf("Authorize(%+v) = %v, want ErrRoleNotGranted", req, err)
		}
	}
}

func TestExtensionBypassDenied(t *testing.T) {
	auth := newTestAuthorizer()

	cases := []Request{
		// 未声明的扩展键
		{
			Namespace: "billing", Operation: "CreateInvoice", Version: "v1",
			Roles: []string{"admin"}, Scope: "invoice",
			Extensions: map[string]string{"bypass-auth": "true"},
		},
		// 已声明扩展键但取值越界
		{
			Namespace: "billing", Operation: "CreateInvoice", Version: "v1",
			Roles: []string{"admin"}, Scope: "invoice",
			Extensions: map[string]string{"mode": "async"},
		},
		// 操作未声明任何扩展却携带扩展
		{
			Namespace: "billing", Operation: "GetInvoice", Version: "v1",
			Roles: []string{"admin"}, Scope: "invoice",
			Extensions: map[string]string{"trace-id": "abc"},
		},
	}
	for _, req := range cases {
		if err := auth.Authorize(req); !errors.Is(err, ErrExtensionNotAllowed) {
			t.Errorf("Authorize(%+v) = %v, want ErrExtensionNotAllowed", req, err)
		}
	}
}

func TestScopeOutOfRangeDenied(t *testing.T) {
	auth := newTestAuthorizer()
	req := Request{
		Namespace: "billing", Operation: "CreateInvoice", Version: "v1",
		Roles: []string{"admin"}, Scope: "customer",
	}
	if err := auth.Authorize(req); !errors.Is(err, ErrScopeNotAllowed) {
		t.Errorf("Authorize(%+v) = %v, want ErrScopeNotAllowed", req, err)
	}
}

func TestMalformedIdentifiersDenied(t *testing.T) {
	auth := newTestAuthorizer()

	cases := []Request{
		{Namespace: "", Operation: "CreateInvoice", Version: "v1", Roles: []string{"admin"}, Scope: "invoice"},
		{Namespace: "billing", Operation: "*", Version: "v1", Roles: []string{"admin"}, Scope: "invoice"},
		{Namespace: "billing", Operation: "CreateInvoice", Version: "", Roles: []string{"admin"}, Scope: "invoice"},
	}
	for _, req := range cases {
		if err := auth.Authorize(req); !errors.Is(err, ErrMalformedRequest) {
			t.Errorf("Authorize(%+v) = %v, want ErrMalformedRequest", req, err)
		}
	}
}

func TestLegitimateCallAllowed(t *testing.T) {
	auth := newTestAuthorizer()

	cases := []Request{
		{
			Namespace: "billing", Operation: "CreateInvoice", Version: "v1",
			Roles: []string{"billing:write"}, Scope: "invoice",
			Extensions: map[string]string{"trace-id": "t-1", "mode": "sync"},
		},
		{
			Namespace: "billing", Operation: "GetInvoice", Version: "v1",
			Roles: []string{"billing:read"}, Scope: "invoice",
		},
	}
	for _, req := range cases {
		if err := auth.Authorize(req); err != nil {
			t.Errorf("Authorize(%+v) = %v, want nil", req, err)
		}
	}
}

func TestBatchAuthorizedIndividually(t *testing.T) {
	auth := newTestAuthorizer()

	reqs := []Request{
		// 合法
		{Namespace: "billing", Operation: "GetInvoice", Version: "v1", Roles: []string{"billing:read"}, Scope: "invoice"},
		// 未声明操作
		{Namespace: "billing", Operation: "DeleteInvoice", Version: "v1", Roles: []string{"admin"}, Scope: "invoice"},
		// 角色越权
		{Namespace: "billing", Operation: "CreateInvoice", Version: "v1", Roles: []string{"billing:read"}, Scope: "invoice"},
		// 扩展绕过
		{
			Namespace: "billing", Operation: "CreateInvoice", Version: "v1",
			Roles: []string{"admin"}, Scope: "invoice",
			Extensions: map[string]string{"admin-override": "1"},
		},
		// 合法
		{
			Namespace: "billing", Operation: "CreateInvoice", Version: "v1",
			Roles: []string{"admin"}, Scope: "invoice",
			Extensions: map[string]string{"mode": "sync"},
		},
	}

	results := auth.AuthorizeBatch(reqs)
	if len(results) != len(reqs) {
		t.Fatalf("AuthorizeBatch returned %d results, want %d", len(results), len(reqs))
	}

	want := []error{nil, ErrOperationNotDeclared, ErrRoleNotGranted, ErrExtensionNotAllowed, nil}
	for i, res := range results {
		if res.Index != i {
			t.Errorf("results[%d].Index = %d", i, res.Index)
		}
		if want[i] == nil {
			if res.Err != nil {
				t.Errorf("results[%d].Err = %v, want nil", i, res.Err)
			}
		} else if !errors.Is(res.Err, want[i]) {
			t.Errorf("results[%d].Err = %v, want %v", i, res.Err, want[i])
		}
	}
}
