package account

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"example.org/netsec/q0292/binder"
)

func init() {
	// 测试期间静默审计日志。
	ProfileSchema = binder.NewSchema(
		"profile.update",
		[]string{"name", "email", "bio", "avatar"},
		[]string{"id", "verified"},
		[]string{"role", "tenant_id", "quota", "audit_trail"},
		binder.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

func baseUser() *User {
	return &User{
		ID:       "u-1",
		Name:     "alice",
		Email:    "alice@example.com",
		Role:     "user",
		TenantID: "t-1",
		Quota:    10,
	}
}

func rejectedReason(res *binder.Result, field string) (string, bool) {
	for _, r := range res.Rejected {
		if r.Field == field {
			return r.Reason, true
		}
	}
	return "", false
}

// 场景一：role/verified 注入——客户端携带服务器字段，必须拒绝且不落库。
func TestRoleInjectionRejected(t *testing.T) {
	u := baseUser()
	svc := NewService(nil)
	res, err := svc.UpdateProfile(u, []byte(
		`{"name":"mallory","role":"admin","verified":true,"tenant_id":"t-9","quota":9999,"audit_trail":"x"}`))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if u.Role != "user" || u.Verified || u.TenantID != "t-1" || u.Quota != 10 || u.AuditTrail != "" {
		t.Fatalf("sensitive fields mutated: %+v", u)
	}
	if u.Name != "mallory" {
		t.Fatalf("whitelisted field not applied: %+v", u)
	}
	for field, want := range map[string]string{
		"role":        binder.ReasonServerOnly,
		"tenant_id":   binder.ReasonServerOnly,
		"quota":       binder.ReasonServerOnly,
		"audit_trail": binder.ReasonServerOnly,
		"verified":    binder.ReasonReadOnly,
	} {
		got, ok := rejectedReason(res, field)
		if !ok || got != want {
			t.Errorf("field %s: got rejection %q/%v, want %q", field, got, ok, want)
		}
	}
}

// 场景二：嵌套对象——对标量字段注入对象必须拒绝。
func TestNestedObjectRejected(t *testing.T) {
	u := baseUser()
	svc := NewService(nil)
	res, err := svc.UpdateProfile(u, []byte(
		`{"name":{"first":"x","last":"y"},"email":["a@b.c"],"bio":"ok"}`))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if u.Name != "alice" || u.Email != "alice@example.com" {
		t.Fatalf("nested object overwrote scalar: %+v", u)
	}
	if u.Bio != "ok" {
		t.Fatalf("legit sibling field not applied: %+v", u)
	}
	for _, field := range []string{"name", "email"} {
		got, ok := rejectedReason(res, field)
		if !ok || got != binder.ReasonNestedObject {
			t.Errorf("field %s: got %q/%v, want nested_object", field, got, ok)
		}
	}
}

// 场景三：大小写变体——"Name"/"ROLE" 等变体键必须拒绝或忽略并记录。
func TestCaseVariantKeysRejected(t *testing.T) {
	u := baseUser()
	svc := NewService(nil)
	res, err := svc.UpdateProfile(u, []byte(
		`{"Name":"mallory","ROLE":"admin","Email":"evil@example.com"}`))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if u.Name != "alice" || u.Role != "user" || u.Email != "alice@example.com" {
		t.Fatalf("case-variant key applied: %+v", u)
	}
	for _, field := range []string{"Name", "ROLE", "Email"} {
		got, ok := rejectedReason(res, field)
		if !ok || got != binder.ReasonCaseVariant {
			t.Errorf("field %s: got %q/%v, want case_variant", field, got, ok)
		}
	}
}

// 场景四：空值覆盖——null 不得清空已有字段。
func TestNullOverwriteRejected(t *testing.T) {
	u := baseUser()
	svc := NewService(nil)
	res, err := svc.UpdateProfile(u, []byte(`{"name":null,"email":null}`))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if u.Name != "alice" || u.Email != "alice@example.com" {
		t.Fatalf("null overwrote field: %+v", u)
	}
	for _, field := range []string{"name", "email"} {
		got, ok := rejectedReason(res, field)
		if !ok || got != binder.ReasonNullValue {
			t.Errorf("field %s: got %q/%v, want null_overwrite", field, got, ok)
		}
	}
}

// 场景五：合法资料更新——白名单字段正常应用，未知字段被记录。
func TestLegitProfileUpdate(t *testing.T) {
	u := baseUser()
	svc := NewService(nil)
	res, err := svc.UpdateProfile(u, []byte(
		`{"name":"alice2","bio":"hello","avatar":"https://img/x.png","nickname":"sneaky"}`))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if u.Name != "alice2" || u.Bio != "hello" || u.Avatar != "https://img/x.png" {
		t.Fatalf("legit update not applied: %+v", u)
	}
	if u.Role != "user" || u.TenantID != "t-1" {
		t.Fatalf("sensitive field touched: %+v", u)
	}
	if len(res.Applied) != 3 {
		t.Errorf("applied = %v, want 3 entries", res.Applied)
	}
	got, ok := rejectedReason(res, "nickname")
	if !ok || got != binder.ReasonUnknownField {
		t.Errorf("nickname: got %q/%v, want unknown_field", got, ok)
	}
}

// 类型错误的白名单字段必须拒绝。
func TestTypeMismatchRejected(t *testing.T) {
	u := baseUser()
	svc := NewService(nil)
	res, err := svc.UpdateProfile(u, []byte(`{"name":123,"bio":"ok"}`))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if u.Name != "alice" || u.Bio != "ok" {
		t.Fatalf("type mismatch applied: %+v", u)
	}
	got, ok := rejectedReason(res, "name")
	if !ok || got != binder.ReasonTypeMismatch {
		t.Errorf("name: got %q/%v, want type_mismatch", got, ok)
	}
}

// 服务器字段只能经独立授权的服务端流程写入。
func TestServerFieldsRequireAuthorization(t *testing.T) {
	u := baseUser()
	svc := NewService(binder.AuthorizerFunc(func(actor, field string) bool {
		return actor == "role-service" && field == "role"
	}))

	// 无授权：整体拒绝。
	noAuth := NewService(nil)
	if _, err := noAuth.AdminSetFields("role-service", u, []byte(`{"role":"admin"}`)); !errors.Is(err, binder.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}

	// 越权字段：整体拒绝，不做部分写入。
	if _, err := svc.AdminSetFields("role-service", u, []byte(`{"role":"admin","quota":9999}`)); !errors.Is(err, binder.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized for quota, got %v", err)
	}
	if u.Role != "user" || u.Quota != 10 {
		t.Fatalf("partial write occurred: %+v", u)
	}

	// 授权内字段：成功写入并记录。
	res, err := svc.AdminSetFields("role-service", u, []byte(`{"role":"admin"}`))
	if err != nil {
		t.Fatalf("authorized write: %v", err)
	}
	if u.Role != "admin" {
		t.Fatalf("authorized role write not applied: %+v", u)
	}
	if len(res.ServerSet) != 1 || res.ServerSet[0] != "role" {
		t.Errorf("ServerSet = %v, want [role]", res.ServerSet)
	}

	// 服务端通道同样拒绝非 ServerOnly 字段。
	if _, err := svc.AdminSetFields("role-service", u, []byte(`{"name":"x"}`)); err == nil {
		t.Fatal("expected error for non-server-only field via server channel")
	}
}
