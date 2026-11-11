package netsec

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

// 小域枚举：在有限但覆盖各选项维度的域上穷举所有
// 「条目对 × 请求 × 选择方式」，验证求值器在任何组合下
// 都不会把不同条目的允许项与限制拼成更宽授权。
func TestEnumerationNoPermissionMixing(t *testing.T) {
	now := testNow

	cidrSets := [][]netip.Prefix{
		nil,
		{mustPrefix(t, "10.0.0.0/8")},
		{mustPrefix(t, "10.1.0.0/16")},
	}
	commands := []string{"", "alpha", "beta"}
	permitSets := [][]string{
		nil,
		{"db:5432"},
		{"db:5432", "web:443"},
	}
	validities := [][2]time.Time{
		{},
		{now.Add(-time.Hour), now.Add(time.Hour)},      // 当前有效
		{now.Add(-2 * time.Hour), now.Add(-time.Hour)}, // 已过期
	}

	var domain []Entry
	for _, cidrs := range cidrSets {
		for _, cmd := range commands {
			for _, po := range permitSets {
				for _, v := range validities {
					domain = append(domain, Entry{
						Fingerprint: fp,
						FromCIDRs:   cidrs,
						Command:     cmd,
						PermitOpen:  po,
						ValidAfter:  v[0],
						ValidBefore: v[1],
					})
				}
			}
		}
	}

	sources := []string{"10.1.2.3", "10.9.9.9", "203.0.113.5"}
	reqCommands := []string{"", "alpha", "beta", "gamma"}
	reqTargets := []string{"", "db:5432", "web:443", "evil:22"}

	var requests []Request
	for _, s := range sources {
		for _, c := range reqCommands {
			for _, tg := range reqTargets {
				requests = append(requests, Request{
					Source:  mustAddr(t, s),
					Command: c,
					Target:  tg,
					Now:     now,
				})
			}
		}
	}

	checked := 0
	for i := range domain {
		for j := range domain {
			entries := []Entry{domain[i], domain[j]}
			entries[0].ID = "e0"
			entries[1].ID = "e1"
			for _, req := range requests {
				cands := Candidates(entries, fp, req)

				// 不变量：候选数 == 单独允许该请求的条目数（合取独立判定）。
				_, ok0 := entries[0].Allows(req)
				_, ok1 := entries[1].Allows(req)
				wantN := 0
				for _, ok := range []bool{ok0, ok1} {
					if ok {
						wantN++
					}
				}
				if len(cands) != wantN {
					t.Fatalf("entries %d,%d req %+v: %d candidates, want %d", i, j, req, len(cands), wantN)
				}

				for _, pick := range []string{"", "e0", "e1", "ghost"} {
					dec, err := Evaluate(entries, fp, req, pick)
					checked++

					if err != nil {
						// 拒绝时必须确实没有可合法给出的授权。
						switch pick {
						case "":
							if wantN == 1 {
								t.Fatalf("entries %d,%d req %+v: single candidate refused: %v", i, j, req, err)
							}
							if wantN == 0 && !errors.Is(err, ErrNoCandidate) {
								t.Fatalf("entries %d,%d req %+v: want ErrNoCandidate, got %v", i, j, req, err)
							}
							if wantN > 1 && !errors.Is(err, ErrAmbiguous) {
								t.Fatalf("entries %d,%d req %+v: want ErrAmbiguous, got %v", i, j, req, err)
							}
							if wantN > 1 {
								conflict := entries[0].Command != entries[1].Command
								if conflict != errors.Is(err, ErrCommandConflict) {
									t.Fatalf("entries %d,%d req %+v: conflict=%v but err=%v", i, j, req, conflict, err)
								}
							}
						default:
							singleOK := pick == "e0" && ok0 || pick == "e1" && ok1
							if wantN > 0 && !singleOK && !errors.Is(err, ErrUnknownSelection) {
								t.Fatalf("entries %d,%d req %+v pick %q: want ErrUnknownSelection, got %v", i, j, req, pick, err)
							}
							if wantN == 0 && !errors.Is(err, ErrNoCandidate) {
								t.Fatalf("entries %d,%d req %+v pick %q: want ErrNoCandidate, got %v", i, j, req, pick, err)
							}
						}
						continue
					}

					// 授权时：证据必须完整来自某一条真实候选条目，
					// 且该条目单独就允许此请求 —— 禁止混权的核心性质。
					ev := dec.Evidence
					var src Entry
					switch ev.EntryID {
					case "e0":
						src = entries[0]
					case "e1":
						src = entries[1]
					default:
						t.Fatalf("entries %d,%d req %+v pick %q: evidence from unknown entry %q", i, j, req, pick, ev.EntryID)
					}
					if _, ok := src.Allows(req); !ok {
						t.Fatalf("entries %d,%d req %+v pick %q: granted but entry %q alone denies (permission mixing!)", i, j, req, pick, ev.EntryID)
					}
					if pick != "" && ev.EntryID != pick {
						t.Fatalf("entries %d,%d req %+v: pick %q but granted %q", i, j, req, pick, ev.EntryID)
					}
					if pick == "" && wantN != 1 {
						t.Fatalf("entries %d,%d req %+v: auto select granted with %d candidates", i, j, req, wantN)
					}
					// 证据完整性：强制命令与有效期必须逐字段等于所选条目。
					if ev.ForcedCommand != src.Command ||
						!ev.ValidAfter.Equal(src.ValidAfter) ||
						!ev.ValidBefore.Equal(src.ValidBefore) ||
						ev.Fingerprint != fp {
						t.Fatalf("entries %d,%d req %+v: evidence %+v not identical to entry %q", i, j, req, ev, ev.EntryID)
					}
				}
			}
		}
	}
	t.Logf("enumerated %d evaluations over %d-entry domain", checked, len(domain))
}

// 显式构造的混权反例：只有拼接两条目选项才会允许的请求，必须被拒绝。
func TestEnumerationMixedRequestAlwaysDenied(t *testing.T) {
	entries := []Entry{
		{ID: "wide-src", Fingerprint: fp, FromCIDRs: []netip.Prefix{mustPrefix(t, "10.0.0.0/8")}, Command: "alpha"},
		{ID: "wide-cmd", Fingerprint: fp, FromCIDRs: []netip.Prefix{mustPrefix(t, "10.1.0.0/16")}, Command: ""},
	}
	// 10.9.9.9 只命中第一条的来源；命令 beta 只被第二条允许。
	req := Request{Source: mustAddr(t, "10.9.9.9"), Command: "beta", Now: testNow}
	for _, pick := range []string{"", "wide-src", "wide-cmd"} {
		if _, err := Evaluate(entries, fp, req, pick); err == nil {
			t.Fatalf("pick %q: mixed permission must be denied", pick)
		} else if pick != "" && !errors.Is(err, ErrUnknownSelection) && !errors.Is(err, ErrNoCandidate) {
			t.Fatalf("pick %q: unexpected error %v", pick, err)
		}
	}
}
