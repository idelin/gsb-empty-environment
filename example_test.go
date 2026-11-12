package taskpkg_test

import (
	"fmt"

	taskpkg "example.org/domainreissue/q002"
)

// fullBodyParser 是宿主侧解析回调的最小示意：把整份请求体当作一个字段的依赖区间。
func fullBodyParser() taskpkg.Parser {
	return func(body []byte) (*taskpkg.Report, error) {
		report := &taskpkg.Report{
			Fields: []taskpkg.FieldOccurrence{{
				Name: "attachment", Raw: taskpkg.Interval{0, len(body)}, Kept: true,
			}},
			Entries: []taskpkg.Entry{{
				Name:   "attachment",
				Policy: taskpkg.PolicyAll,
				Chosen: taskpkg.FieldOccurrence{
					Name: "attachment", Raw: taskpkg.Interval{0, len(body)}, Kept: true,
				},
				DependOn: []taskpkg.Interval{{0, len(body)}},
			}},
			Audited: []taskpkg.Interval{{0, len(body)}},
		}
		return report, nil
	}
}

func ExampleSearcher() {
	base := []byte("MULTIPART-BODY")
	// 网关默认审核整份体；插入两个哨兵后，网关把哨兵之间的字节视为非附件而放行，
	// 后端始终执行整份体，从而形成覆盖缺口。
	const s1, s2 = "[S1]", "[S2]"
	gateway := func(body []byte) (*taskpkg.Report, error) {
		r, _ := fullBodyParser()(body)
		i1, i2 := indexOf(body, s1), indexOf(body, s2)
		if i1 >= 0 && i2 >= 0 {
			r.Audited = []taskpkg.Interval{{0, i1 + len(s1)}, {i2, len(body)}}
			r.Entries[0].DependOn = r.Audited
		}
		return r, nil
	}
	backend := fullBodyParser()

	candidates := []taskpkg.Candidate{
		{Kind: taskpkg.KindInsert, At: 4, Payload: []byte(s1), Note: "boundary sentinel one"},
		{Kind: taskpkg.KindInsert, At: 12, Payload: []byte(s2), Note: "boundary sentinel two"},
	}
	report, err := (taskpkg.Searcher{
		Base: base, Candidates: candidates, Gateway: gateway, Backend: backend,
	}).Search()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if report == nil {
		fmt.Println("no bypass found")
		return
	}
	fmt.Println("minimum changes:", report.MinimumChanges)
	fmt.Println("picks:", report.Picks)
	fmt.Println("reason:", report.Verdict.Reason)
	fmt.Println("uncovered:", report.Uncovered)
	// Output:
	// minimum changes: 2
	// picks: [0 1]
	// reason: backend execution dependency not fully covered by audited bytes
	// uncovered: [{8 16}]
}

func indexOf(body []byte, sub string) int {
	for i := 0; i+len(sub) <= len(body); i++ {
		if string(body[i:i+len(sub)]) == sub {
			return i
		}
	}
	return -1
}
