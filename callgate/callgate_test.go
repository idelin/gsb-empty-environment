package callgate

import (
	"sync"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(g *Guard)
		call    Call
		allowed bool
		reason  string
		matched string
	}{
		{
			name:    "大小写变形 SyStEm 直接调用仍禁用",
			call:    Call{Function: "SyStEm", Kind: KindDirect, Args: []string{"id"}},
			allowed: false,
			reason:  ReasonDisabledFunction,
			matched: "system",
		},
		{
			name:    "前导反斜杠 \\system 仍禁用",
			call:    Call{Function: `\system`, Kind: KindDirect},
			allowed: false,
			reason:  ReasonDisabledFunction,
			matched: "system",
		},
		{
			name:    "命名空间前缀 \\Foo\\Bar\\SYSTEM 仍禁用",
			call:    Call{Function: `\Foo\Bar\SYSTEM`, Kind: KindDirect},
			allowed: false,
			reason:  ReasonDisabledFunction,
			matched: "system",
		},
		{
			name:    "可变函数调用 system 仍禁用",
			call:    Call{Function: "system", Kind: KindVariable, Args: []string{"id"}},
			allowed: false,
			reason:  ReasonDisabledFunction,
			matched: "system",
		},
		{
			name:    "Class::popen 只查方法名即命中",
			call:    Call{Function: "App\\Runner::Popen", Kind: KindDirect},
			allowed: false,
			reason:  ReasonDisabledFunction,
			matched: "popen",
		},
		{
			name: "call_user_func 字符串回调 system 被回调规则拦截",
			call: Call{
				Function: "call_user_func",
				Kind:     KindCallUserFunc,
				Callback: &Callback{Kind: CallbackString, Name: "system"},
				Args:     []string{"system", "id"},
			},
			allowed: false,
			reason:  ReasonDisabledCallback,
			matched: "system",
		},
		{
			name: "call_user_func 的 Class::method 只查方法名",
			call: Call{
				Function: "call_user_func",
				Kind:     KindCallUserFunc,
				Callback: &Callback{Kind: CallbackString, Name: "SafeClass::PaSsThRu"},
			},
			allowed: false,
			reason:  ReasonDisabledCallback,
			matched: "passthru",
		},
		{
			name: "array_map 字符串回调 shell_exec 被拦截",
			call: Call{
				Function: "array_map",
				Kind:     KindArrayMap,
				Callback: &Callback{Kind: CallbackString, Name: "shell_exec"},
				Args:     []string{"shell_exec", "id"},
			},
			allowed: false,
			reason:  ReasonDisabledCallback,
			matched: "shell_exec",
		},
		{
			name: "数组回调只取方法名检查 exec",
			call: Call{
				Function: "array_map",
				Kind:     KindArrayMap,
				Callback: &Callback{Kind: CallbackArray, Class: "SafeClass", Method: "EXEC"},
			},
			allowed: false,
			reason:  ReasonDisabledCallback,
			matched: "exec",
		},
		{
			name: "数组回调的无害方法放行",
			call: Call{
				Function: "array_map",
				Kind:     KindArrayMap,
				Callback: &Callback{Kind: CallbackArray, Class: "SafeClass", Method: "trim"},
			},
			allowed: true,
		},
		{
			name: "闭包回调没有字符串名字，放行",
			call: Call{
				Function: "call_user_func",
				Kind:     KindCallUserFunc,
				Callback: &Callback{Kind: CallbackClosure},
			},
			allowed: true,
		},
		{
			name:    "strlen 等无关调用放行",
			call:    Call{Function: "strlen", Kind: KindDirect, Args: []string{"abc"}},
			allowed: true,
		},
		{
			name:    "未污染时 mail 放行",
			call:    Call{Function: "mail", Kind: KindDirect},
			allowed: true,
		},
		{
			name:  "LD_PRELOAD 后调用 mail 拒绝",
			setup: func(g *Guard) { g.Check(Call{Function: "putenv", Args: []string{"LD_PRELOAD=/tmp/evil.so"}}) },
			call:  Call{Function: "mail", Kind: KindDirect},

			allowed: false,
			reason:  ReasonTaintedMailer,
			matched: "mail",
		},
		{
			name:  "LD_LIBRARY_PATH 后 mb_send_mail 拒绝",
			setup: func(g *Guard) { g.Check(Call{Function: "putenv", Args: []string{"LD_LIBRARY_PATH=/tmp/evil"}}) },
			call:  Call{Function: "mb_send_mail", Kind: KindDirect},

			allowed: false,
			reason:  ReasonTaintedMailer,
			matched: "mb_send_mail",
		},
		{
			name:  "LD_PRELOAD 后 imap_open 拒绝",
			setup: func(g *Guard) { g.Check(Call{Function: "putenv", Args: []string{"LD_PRELOAD=evil.so"}}) },
			call:  Call{Function: "imap_open", Kind: KindDirect},

			allowed: false,
			reason:  ReasonTaintedMailer,
			matched: "imap_open",
		},
		{
			name:  "无关环境变量不污染，mail 放行",
			setup: func(g *Guard) { g.Check(Call{Function: "putenv", Args: []string{"APP_ENV=prod"}}) },
			call:  Call{Function: "mail", Kind: KindDirect},

			allowed: true,
		},
		{
			name:  "小写变量名与删除形式同样产生污染（fail-closed）",
			setup: func(g *Guard) { g.Check(Call{Function: "putenv", Args: []string{" ld_preload "}}) },
			call:  Call{Function: "Mail", Kind: KindDirect},

			allowed: false,
			reason:  ReasonTaintedMailer,
			matched: "mail",
		},
		{
			name: "污染是粘性的，后续普通调用不清除状态",
			setup: func(g *Guard) {
				g.Check(Call{Function: "putenv", Args: []string{"LD_PRELOAD=evil.so"}})
				g.Check(Call{Function: "strlen", Kind: KindDirect, Args: []string{"x"}})
			},
			call: Call{Function: `\mb_send_mail`, Kind: KindVariable},

			allowed: false,
			reason:  ReasonTaintedMailer,
			matched: "mb_send_mail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGuard()
			if tt.setup != nil {
				tt.setup(g)
			}
			got := g.Check(tt.call)
			if got.Allowed != tt.allowed {
				t.Fatalf("Allowed = %v, want %v (reason=%q matched=%q)", got.Allowed, tt.allowed, got.Reason, got.Matched)
			}
			if !tt.allowed {
				if got.Reason != tt.reason {
					t.Fatalf("Reason = %q, want %q", got.Reason, tt.reason)
				}
				if got.Matched != tt.matched {
					t.Fatalf("Matched = %q, want %q", got.Matched, tt.matched)
				}
			}
		})
	}
}

func TestDisabledSetExpanded(t *testing.T) {
	for _, fn := range []string{
		"system", "exec", "passthru", "shell_exec",
		"popen", "proc_open", "pcntl_exec",
	} {
		t.Run(fn, func(t *testing.T) {
			got := NewGuard().Check(Call{Function: fn, Kind: KindDirect})
			if got.Allowed || got.Matched != fn {
				t.Fatalf("%s 未被禁用: %+v", fn, got)
			}
			if !IsDisabled(`\ns\` + fn) {
				t.Fatalf("IsDisabled 对带命名空间的 %s 返回 false", fn)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"SyStEm":            "system",
		`\system`:           "system",
		`\ns\sub\Proc_Open`: "proc_open",
		"SafeClass::system": "system",
		"SafeClass::EXEC()": "exec",
		"  strlen  ":        "strlen",
	}
	for in, want := range cases {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaintedFlag(t *testing.T) {
	g := NewGuard()
	if g.Tainted() {
		t.Fatal("新建 Guard 不应处于污染状态")
	}
	g.Check(Call{Function: "putenv", Args: []string{"PATH=/usr/bin"}})
	if g.Tainted() {
		t.Fatal("设置 PATH 不应污染")
	}
	g.Check(Call{Function: "putenv", Args: []string{"LD_LIBRARY_PATH=/x"}})
	if !g.Tainted() {
		t.Fatal("设置 LD_LIBRARY_PATH 后应处于污染状态")
	}
}

func TestConcurrentCheck(t *testing.T) {
	g := NewGuard()
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Check(Call{Function: "putenv", Args: []string{"LD_PRELOAD=evil.so"}})
			if d := g.Check(Call{Function: "system", Kind: KindVariable}); d.Allowed {
				t.Error("并发检查中 system 被错误放行")
			}
			if d := g.Check(Call{Function: "mail"}); d.Allowed {
				t.Error("并发检查中污染后的 mail 被错误放行")
			}
		}()
	}
	wg.Wait()
}
