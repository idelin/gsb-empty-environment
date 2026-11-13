package scope_test

import (
	"reflect"
	"testing"

	"example.org/netsec/q0643/scope"
)

func grantRepo(repository string, actions ...string) scope.Grant {
	if actions == nil {
		actions = []string{}
	}
	return scope.Grant{Type: scope.TypeRepository, Repository: repository, Actions: actions}
}

func grantCatalog(wildcard bool) scope.Grant {
	if wildcard {
		return scope.Grant{Type: scope.TypeRegistry, Actions: []string{"*"}, Wildcard: true}
	}
	return scope.Grant{Type: scope.TypeRegistry, Actions: []string{}}
}

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name        string
		scopes      []string
		permissions []scope.Permission
		want        []scope.Grant
		wantStrings []string
	}{
		{
			name:   "路径穿越 team-a/../team-b 整条丢弃",
			scopes: []string{"repository:team-a/../team-b:push,pull"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-b/*", Actions: []string{"pull", "push"}},
			},
			want:        []scope.Grant{},
			wantStrings: nil,
		},
		{
			name:   "通配动作 * 整条丢弃",
			scopes: []string{"repository:team-a/app:pull,*"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"pull", "push"}},
			},
			want:        []scope.Grant{},
			wantStrings: nil,
		},
		{
			name:   "同一仓库重复 scope 先并集再求交",
			scopes: []string{"repository:team-a/app:pull", "repository:team-a/app:push"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"pull", "push"}},
			},
			want:        []scope.Grant{grantRepo("team-a/app", "pull", "push")},
			wantStrings: []string{"repository:team-a/app:pull,push"},
		},
		{
			name:   "push 无 pull 时撤掉写动作",
			scopes: []string{"repository:team-a/app:push"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"push"}},
			},
			want:        []scope.Grant{grantRepo("team-a/app")},
			wantStrings: []string{"repository:team-a/app:"},
		},
		{
			name:   "delete 无 pull 时撤掉写动作",
			scopes: []string{"repository:team-a/app:delete"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"delete"}},
			},
			want:        []scope.Grant{grantRepo("team-a/app")},
			wantStrings: []string{"repository:team-a/app:"},
		},
		{
			name:   "空交集 scope 保留且动作集为空",
			scopes: []string{"repository:team-b/app:pull"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"pull", "push"}},
			},
			want:        []scope.Grant{grantRepo("team-b/app")},
			wantStrings: []string{"repository:team-b/app:"},
		},
		{
			name:        "无列表权限时 catalog 保留为空动作集",
			scopes:      []string{"registry:catalog:*"},
			permissions: []scope.Permission{{RepositoryPattern: "team-a/*", Actions: []string{"pull"}}},
			want:        []scope.Grant{grantCatalog(false)},
			wantStrings: []string{"registry:catalog:"},
		},
		{
			name:   "有列表权限时 catalog 授予通配",
			scopes: []string{"registry:catalog:*"},
			permissions: []scope.Permission{
				{RepositoryPattern: "registry:catalog:*", Actions: []string{"list"}},
			},
			want:        []scope.Grant{grantCatalog(true)},
			wantStrings: []string{"registry:catalog:*"},
		},
		{
			name:   "分段前缀不越过命名空间边界",
			scopes: []string{"repository:team-ab/app:pull,push"},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"pull", "push"}},
			},
			want:        []scope.Grant{grantRepo("team-ab/app")},
			wantStrings: []string{"repository:team-ab/app:"},
		},
		{
			name: "合法多 scope 求交且动作按规范顺序输出",
			scopes: []string{
				"repository:team-a/app:delete,pull,push",
				"repository:team-b/lib:pull",
			},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"pull", "push", "delete"}},
				{RepositoryPattern: "team-b/lib", Actions: []string{"pull"}},
			},
			want: []scope.Grant{
				grantRepo("team-a/app", "pull", "push", "delete"),
				grantRepo("team-b/lib", "pull"),
			},
			wantStrings: []string{
				"repository:team-a/app:pull,push,delete",
				"repository:team-b/lib:pull",
			},
		},
		{
			name: "全局 * 与精确权限合并后仍受 pull 约束",
			scopes: []string{
				"repository:team-a/app:pull,push",
				"repository:team-b/app:push",
			},
			permissions: []scope.Permission{
				{RepositoryPattern: "*", Actions: []string{"pull", "push"}},
				{RepositoryPattern: "team-b/app", Actions: []string{"push"}},
			},
			want: []scope.Grant{
				grantRepo("team-a/app", "pull", "push"),
				grantRepo("team-b/app", "push"),
			},
			wantStrings: []string{
				"repository:team-a/app:pull,push",
				"repository:team-b/app:push",
			},
		},
		{
			name: "混合非法与合法 scope，非法丢弃且按首次出现排序",
			scopes: []string{
				"repository:Team-A/app:pull",
				"repository:team-a/app:pull",
				"repository:team-a//app:pull",
				"registry:other:*",
				"repository:team-b/lib:pull",
			},
			permissions: []scope.Permission{
				{RepositoryPattern: "team-a/*", Actions: []string{"pull"}},
			},
			want: []scope.Grant{
				grantRepo("team-a/app", "pull"),
				grantRepo("team-b/lib"),
			},
			wantStrings: []string{
				"repository:team-a/app:pull",
				"repository:team-b/lib:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scope.Authorize(tt.scopes, tt.permissions)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Authorize() = %#v, want %#v", got, tt.want)
			}
			if tt.wantStrings != nil {
				gotStrings := make([]string, len(got))
				for i, grant := range got {
					gotStrings[i] = grant.String()
				}
				if !reflect.DeepEqual(gotStrings, tt.wantStrings) {
					t.Fatalf("String() = %v, want %v", gotStrings, tt.wantStrings)
				}
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want scope.Scope
		ok   bool
	}{
		{
			name: "合法仓库 scope",
			raw:  "repository:team-a/app.web_1:push,pull",
			want: scope.Scope{Type: scope.TypeRepository, Repository: "team-a/app.web_1", Actions: []string{"pull", "push"}},
			ok:   true,
		},
		{name: "单段仓库名合法", raw: "repository:app:pull", want: scope.Scope{Type: scope.TypeRepository, Repository: "app", Actions: []string{"pull"}}, ok: true},
		{name: "catalog 字面量", raw: "registry:catalog:*", want: scope.Scope{Type: scope.TypeRegistry, Actions: []string{"*"}}, ok: true},
		{name: "动作重复自动去重", raw: "repository:team-a/app:pull,pull", want: scope.Scope{Type: scope.TypeRepository, Repository: "team-a/app", Actions: []string{"pull"}}, ok: true},
		{name: "大写仓库名拒绝", raw: "repository:Team-A/app:pull", ok: false},
		{name: "点号双段拒绝", raw: "repository:team-a/../app:pull", ok: false},
		{name: "空段拒绝", raw: "repository:team-a//app:pull", ok: false},
		{name: "非法字符拒绝", raw: "repository:team-a/app name:pull", ok: false},
		{name: "未知动作拒绝", raw: "repository:team-a/app:admin", ok: false},
		{name: "通配动作拒绝", raw: "repository:team-a/app:*", ok: false},
		{name: "空动作拒绝", raw: "repository:team-a/app:", ok: false},
		{name: "字段数不对拒绝", raw: "repository:team-a/app:pull:extra", ok: false},
		{name: "未知资源类型拒绝", raw: "registry:other:*", ok: false},
		{name: "空字符串拒绝", raw: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := scope.Parse(tt.raw)
			if ok != tt.ok {
				t.Fatalf("Parse(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestAuthorizeEmptyInputs(t *testing.T) {
	if got := scope.Authorize(nil, nil); len(got) != 0 {
		t.Fatalf("Authorize(nil, nil) = %#v, want empty", got)
	}
}
