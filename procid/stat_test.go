package procid

import (
	"errors"
	"testing"
)

func TestParseStat(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantPID   int
		wantComm  string
		wantStart uint64
	}{
		{
			name:      "normal comm",
			line:      statLine(4242, "sshd", 123456),
			wantPID:   4242,
			wantComm:  "sshd",
			wantStart: 123456,
		},
		{
			// 攻击者把进程名改成带空格和右括号的串，试图让解析器
			// 提前切字段、把 starttime 读成 0 或其它字段。
			name:      "forged comm with spaces and parens",
			line:      statLine(4242, "ev)l (x) ) rm -rf ", 987654321),
			wantPID:   4242,
			wantComm:  "ev)l (x) ) rm -rf ",
			wantStart: 987654321,
		},
		{
			name:      "unicode comm",
			line:      statLine(7, "守护 进程)", 5),
			wantPID:   7,
			wantComm:  "守护 进程)",
			wantStart: 5,
		},
		{
			name:      "empty comm",
			line:      statLine(1, "", 11),
			wantPID:   1,
			wantComm:  "",
			wantStart: 11,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := ParseStat(tc.line)
			if err != nil {
				t.Fatalf("ParseStat: %v", err)
			}
			if info.PID != tc.wantPID || info.Comm != tc.wantComm ||
				info.Starttime != tc.wantStart {
				t.Fatalf("got %+v, want pid=%d comm=%q start=%d",
					info, tc.wantPID, tc.wantComm, tc.wantStart)
			}
		})
	}
}

func TestParseStatErrors(t *testing.T) {
	bad := []string{
		"",
		"1234 no parens S 1",
		"(comm) S 1",           // 缺 PID
		"abc (comm) S 1",       // PID 非数字
		"123 (comm) S",         // 字段不足，没有 starttime
		"123 (comm) S 1 2 3 x", // 字段不足
		"123 (comm) " + padFields("not-a-number"),
	}
	for _, line := range bad {
		if _, err := ParseStat(line); !errors.Is(err, ErrStatUnparseable) {
			t.Fatalf("line %q: got err=%v, want ErrStatUnparseable", line, err)
		}
	}
}

// padFields 制造字段足够多但 starttime 位置非法的后缀。
func padFields(startToken string) string {
	tokens := make([]string, 20)
	for i := range tokens {
		tokens[i] = "0"
	}
	tokens[19] = startToken
	out := tokens[0]
	for _, tok := range tokens[1:] {
		out += " " + tok
	}
	return out
}

func TestIsExeDeleted(t *testing.T) {
	if !IsExeDeleted("/usr/bin/evild (deleted)") {
		t.Fatal("deleted exe not detected")
	}
	if IsExeDeleted("/usr/bin/legitd") {
		t.Fatal("normal exe misclassified as deleted")
	}
	if got := normalizeExe("/usr/bin/evild (deleted)"); got != "/usr/bin/evild" {
		t.Fatalf("normalizeExe = %q", got)
	}
}
