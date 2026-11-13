package netsec

import "testing"

var testWhitelist = []string{"app.safe", "media.store"}

func decisionName(d Decision) string {
	switch d {
	case DecisionForward:
		return "Forward"
	case DecisionStripFlags:
		return "StripFlags"
	case DecisionReject:
		return "Reject"
	}
	return "Unknown"
}

func assertDecision(t *testing.T, intent *Intent, want Decision) {
	t.Helper()
	if got := Evaluate(intent, testWhitelist); got != want {
		t.Fatalf("Evaluate() = %s, want %s", decisionName(got), decisionName(want))
	}
}

// data 合法但 ClipData 夹带受保护地址，授权对每一项同样生效。
func TestClipDataSmuggling(t *testing.T) {
	intent := &Intent{
		Flags: FlagGrantReadURI,
		Data:  "content://app.safe/docs/1",
		ClipData: []string{
			"content://app.safe/ok",
			"content://protected.provider/secret",
		},
	}
	assertDecision(t, intent, DecisionStripFlags)
}

// 用户信息伪装：content://app.safe@evil/x 的 authority 是 evil。
func TestUserinfoDisguise(t *testing.T) {
	assertDecision(t, &Intent{
		Flags: FlagGrantReadURI,
		Data:  "content://app.safe@evil/x",
	}, DecisionStripFlags)

	// 反向：用户信息是 evil 但真实 authority 在白名单内，可放行。
	assertDecision(t, &Intent{
		Flags: FlagGrantReadURI,
		Data:  "content://evil@app.safe/x",
	}, DecisionForward)
}

// 嵌套子意图中的地址同样受授权约束。
func TestNestedChildIntent(t *testing.T) {
	intent := &Intent{
		Flags: FlagGrantReadURI,
		Data:  "content://app.safe/docs/1",
		Extras: []*Intent{{
			Data: "content://app.safe/ok",
			Extras: []*Intent{{
				Data: "content://protected.provider/secret",
			}},
		}},
	}
	assertDecision(t, intent, DecisionStripFlags)
}

// 嵌套 3 层可接受，超过 3 层拒绝。
func TestNestedDepthLimit(t *testing.T) {
	chain := func(levels int) *Intent {
		root := &Intent{Data: "content://app.safe/x"}
		cur := root
		for i := 0; i < levels; i++ {
			child := &Intent{Data: "content://app.safe/x"}
			cur.Extras = []*Intent{child}
			cur = child
		}
		return root
	}
	assertDecision(t, chain(3), DecisionForward)
	assertDecision(t, chain(4), DecisionReject)
}

// file 协议一律拒绝，与授权标志无关。
func TestFileSchemeRejected(t *testing.T) {
	assertDecision(t, &Intent{Data: "file:///sdcard/secret.txt"}, DecisionReject)
	assertDecision(t, &Intent{Data: "FILE:///etc/passwd"}, DecisionReject)
	assertDecision(t, &Intent{
		Flags:    FlagGrantReadURI,
		Data:     "content://app.safe/docs/1",
		ClipData: []string{"file:///data/data/other.app/databases/creds.db"},
	}, DecisionReject)
	assertDecision(t, &Intent{
		Extras: []*Intent{{Data: "file:///sdcard/x"}},
	}, DecisionReject)
}

// 清除授权标志后即可转发。
func TestStripThenForward(t *testing.T) {
	intent := &Intent{
		Flags: FlagGrantReadURI | FlagGrantWriteURI | 0x40,
		Data:  "content://protected.provider/secret",
		Extras: []*Intent{{
			Flags: FlagGrantPrefixURI,
			Data:  "content://protected.provider/nested",
		}},
	}
	assertDecision(t, intent, DecisionStripFlags)

	stripped := StripGrantFlags(intent)
	assertDecision(t, stripped, DecisionForward)

	// 授权位被清除，其余标志位保留，原意图不被修改。
	if stripped.Flags != 0x40 {
		t.Fatalf("stripped.Flags = %#x, want 0x40", stripped.Flags)
	}
	if stripped.Extras[0].Flags != 0 {
		t.Fatalf("stripped child Flags = %#x, want 0", stripped.Extras[0].Flags)
	}
	if intent.Flags != FlagGrantReadURI|FlagGrantWriteURI|0x40 {
		t.Fatalf("original intent mutated: Flags = %#x", intent.Flags)
	}
}

// 读、写、前缀任一授权标志都触发白名单核对。
func TestEachGrantFlagTriggersCheck(t *testing.T) {
	for _, flag := range []int{FlagGrantReadURI, FlagGrantWriteURI, FlagGrantPrefixURI} {
		intent := &Intent{Flags: flag, Data: "content://protected.provider/x"}
		if got := Evaluate(intent, testWhitelist); got != DecisionStripFlags {
			t.Fatalf("flag %#x: Evaluate() = %s, want StripFlags", flag, decisionName(got))
		}
	}
}

// 全部地址都在白名单内时按原样转发；authority 大小写与端口不敏感。
func TestAllWhitelistedForwards(t *testing.T) {
	assertDecision(t, &Intent{
		Flags: FlagGrantReadURI | FlagGrantWriteURI,
		Data:  "content://APP.SAFE/docs/1",
		ClipData: []string{
			"content://media.store:8080/img/2",
			"content://user@app.safe/x",
		},
	}, DecisionForward)
}

// content 之外的 scheme 不要求白名单。
func TestNonContentSchemeNoWhitelist(t *testing.T) {
	assertDecision(t, &Intent{
		Flags: FlagGrantReadURI,
		Data:  "https://evil.example.com/x",
	}, DecisionForward)
}

// 无授权标志时，content 地址不受白名单约束。
func TestNoGrantFlagsNoCheck(t *testing.T) {
	assertDecision(t, &Intent{
		Data: "content://protected.provider/secret",
	}, DecisionForward)
}

// 子意图自身带授权标志时，其地址同样受白名单约束。
func TestChildGrantFlagsChecked(t *testing.T) {
	assertDecision(t, &Intent{
		Data: "content://app.safe/docs/1",
		Extras: []*Intent{{
			Flags: FlagGrantReadURI,
			Data:  "content://protected.provider/secret",
		}},
	}, DecisionStripFlags)
}
