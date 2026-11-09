package main

import (
	"fmt"
	"testing"
)

func TestFirstFailure(t *testing.T) {
	// 41-link chain l0->l1->...->l41, then F l0 trips the jump limit.
	chain := [][]string{}
	for i := 0; i <= 40; i++ {
		chain = append(chain, []string{"L", fmt.Sprintf("l%d", i), fmt.Sprintf("l%d", i+1)})
	}
	chain = append(chain, []string{"F", "l0"})

	// 40-link chain l0->...->l40, then F l0 stays within the limit.
	chainOK := [][]string{}
	for i := 0; i <= 39; i++ {
		chainOK = append(chainOK, []string{"L", fmt.Sprintf("k%d", i), fmt.Sprintf("k%d", i+1)})
	}
	chainOK = append(chainOK, []string{"F", "k0"})

	cases := []struct {
		name string
		ops  [][]string
		want int
	}{
		{
			name: "implicit parents and plain write",
			ops:  [][]string{{"F", "a/b/c"}, {"F", "a/b/d"}},
			want: -1,
		},
		{
			name: "absolute write path rejected",
			ops:  [][]string{{"F", "ok"}, {"F", "/etc/passwd"}},
			want: 1,
		},
		{
			name: "absolute link write path rejected",
			ops:  [][]string{{"L", "/abs", "x"}},
			want: 0,
		},
		{
			name: "dotdot escapes root",
			ops:  [][]string{{"F", "../x"}},
			want: 0,
		},
		{
			name: "dotdot at root boundary ok",
			ops:  [][]string{{"F", "a/../b"}},
			want: -1,
		},
		{
			name: "absolute link target followed",
			ops:  [][]string{{"L", "a", "/etc"}, {"F", "a/x"}},
			want: 1,
		},
		{
			name: "absolute link target created but never followed",
			ops:  [][]string{{"L", "a", "/etc"}, {"F", "b"}},
			want: -1,
		},
		{
			name: "chained links followed by F",
			ops:  [][]string{{"L", "a", "b"}, {"L", "b", "c"}, {"F", "a"}},
			want: -1,
		},
		{
			name: "chain through intermediate segment",
			ops:  [][]string{{"L", "a", "b"}, {"L", "b", "c"}, {"F", "a/d/e"}},
			want: -1,
		},
		{
			name: "overwrite old link retargets",
			ops:  [][]string{{"L", "a", "/abs"}, {"L", "a", "ok"}, {"F", "a"}},
			want: -1,
		},
		{
			name: "stale link without overwrite fails",
			ops:  [][]string{{"L", "a", "/abs"}, {"F", "a"}},
			want: 1,
		},
		{
			name: "L does not follow final link",
			ops:  [][]string{{"L", "a", "b"}, {"L", "a", "c"}, {"F", "a"}},
			want: -1,
		},
		{
			name: "link target dotdot resolved after substitution",
			ops:  [][]string{{"L", "a/b", "../c"}, {"F", "a/b/d"}},
			want: -1,
		},
		{
			name: "link target escapes root",
			ops:  [][]string{{"L", "a", "../../x"}, {"F", "a"}},
			want: 1,
		},
		{
			name: "jump limit exceeded",
			ops:  chain,
			want: 41,
		},
		{
			name: "jump limit exactly reached ok",
			ops:  chainOK,
			want: -1,
		},
		{
			name: "self link exceeds jumps not infinite",
			ops:  [][]string{{"L", "a", "a"}, {"F", "a"}},
			want: 1,
		},
		{
			name: "write through file rejected",
			ops:  [][]string{{"F", "a"}, {"F", "a/b"}},
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstFailure(tc.ops); got != tc.want {
				t.Fatalf("firstFailure = %d, want %d", got, tc.want)
			}
		})
	}
}
