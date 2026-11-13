package netsec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// hasCode 判断报告中是否包含指定违规码。
func hasCode(rep Report, code string) bool {
	return slices.ContainsFunc(rep.Violations, func(v Violation) bool {
		return v.Code == code
	})
}

// safePath 返回一个对当前用户不可写的 PATH 目录（不存在的系统目录兜底）。
func safePath() string {
	candidates := []string{"/usr/sbin", "/sbin", "/usr/bin"}
	for _, dir := range candidates {
		ok, err := isWritableDir(dir)
		if err == nil && !ok {
			return dir
		}
	}
	return "/nonexistent-safe-dir"
}

// TestTaintMismatch 验证首行带 -T 但启动参数缺 -T 时判定不一致。
func TestTaintMismatch(t *testing.T) {
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl -T",
		Argv:    []string{"/usr/bin/perl", "upload.pl"},
		Env:     []string{"PATH=" + safePath()},
	}
	rep := CheckLaunch(in)
	if rep.OK {
		t.Fatalf("期望拒绝不一致启动，实际通过: %+v", rep.Violations)
	}
	if !hasCode(rep, ErrTaintMismatch) {
		t.Fatalf("期望违规码 %s，实际: %+v", ErrTaintMismatch, rep.Violations)
	}
}

// TestTaintMismatchReverse 验证首行无 -T 但启动参数带 -T 同样不一致。
func TestTaintMismatchReverse(t *testing.T) {
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl",
		Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:     []string{"PATH=" + safePath()},
	}
	rep := CheckLaunch(in)
	if !hasCode(rep, ErrTaintMismatch) {
		t.Fatalf("期望违规码 %s，实际: %+v", ErrTaintMismatch, rep.Violations)
	}
}

// TestOnlyTNotEnough 验证仅 -t（taint warnings）不算污点模式。
func TestOnlyTNotEnough(t *testing.T) {
	cases := []struct {
		name    string
		shebang string
		argv    []string
	}{
		{
			name:    "both_t",
			shebang: "#!/usr/bin/perl -t",
			argv:    []string{"/usr/bin/perl", "-t", "upload.pl"},
		},
		{
			name:    "combined_t",
			shebang: "#!/usr/bin/perl -wt",
			argv:    []string{"/usr/bin/perl", "-wt", "upload.pl"},
		},
		{
			name:    "shebang_T_argv_t",
			shebang: "#!/usr/bin/perl -T",
			argv:    []string{"/usr/bin/perl", "-t", "upload.pl"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := LaunchInput{
				Shebang: tc.shebang,
				Argv:    tc.argv,
				Env:     []string{"PATH=" + safePath()},
			}
			rep := CheckLaunch(in)
			if rep.OK {
				t.Fatalf("期望拒绝仅 -t 启动，实际通过: %+v", rep.Violations)
			}
			if !hasCode(rep, ErrTaintNotStrong) && !hasCode(rep, ErrTaintMismatch) {
				t.Fatalf("期望 %s 或 %s，实际: %+v",
					ErrTaintNotStrong, ErrTaintMismatch, rep.Violations)
			}
		})
	}
}

// TestPERL5OPTReject 验证拒绝策略下 PERL5OPT/PERL5LIB 注入被拦截。
func TestPERL5OPTReject(t *testing.T) {
	in := LaunchInput{
		Shebang:       "#!/usr/bin/perl -T",
		Argv:          []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:           []string{"PATH=" + safePath(), "PERL5OPT=-Mevil", "PERL5LIB=/tmp/evil"},
		PerlEnvPolicy: EnvReject,
	}
	rep := CheckLaunch(in)
	if !hasCode(rep, ErrPerl5Opt) {
		t.Fatalf("期望违规码 %s，实际: %+v", ErrPerl5Opt, rep.Violations)
	}
	if !hasCode(rep, ErrPerl5Lib) {
		t.Fatalf("期望违规码 %s，实际: %+v", ErrPerl5Lib, rep.Violations)
	}
	if !slices.Equal(rep.CleanedEnv, in.Env) {
		t.Fatalf("拒绝策略下不应修改原环境，CleanedEnv: %v", rep.CleanedEnv)
	}
}

// TestPERL5OPTSanitize 验证清除策略下两个变量被移除且检查通过。
func TestPERL5OPTSanitize(t *testing.T) {
	in := LaunchInput{
		Shebang:       "#!/usr/bin/perl -T",
		Argv:          []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:           []string{"PATH=" + safePath(), "PERL5OPT=-Mevil", "PERL5LIB=/tmp/evil", "FOO=bar"},
		PerlEnvPolicy: EnvSanitize,
	}
	rep := CheckLaunch(in)
	if !rep.OK {
		t.Fatalf("清除策略下应通过，实际: %+v", rep.Violations)
	}
	want := []string{"PATH=" + safePath(), "FOO=bar"}
	if !slices.Equal(rep.CleanedEnv, want) {
		t.Fatalf("清理后环境不匹配，期望 %v，实际 %v", want, rep.CleanedEnv)
	}
}

// TestPATHContainsCurrentDir 验证 PATH 含当前目录（. 与空条目）时拒绝。
func TestPATHContainsCurrentDir(t *testing.T) {
	cases := []string{
		".:" + safePath(),
		safePath() + ":.",
		safePath() + "::/bin",
	}
	for _, path := range cases {
		in := LaunchInput{
			Shebang: "#!/usr/bin/perl -T",
			Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
			Env:     []string{"PATH=" + path},
		}
		rep := CheckLaunch(in)
		if !hasCode(rep, ErrPathCurrentDir) {
			t.Fatalf("PATH=%q 期望违规码 %s，实际: %+v",
				path, ErrPathCurrentDir, rep.Violations)
		}
	}
}

// TestPATHContainsWritableDir 验证 PATH 含当前用户可写目录时拒绝。
func TestPATHContainsWritableDir(t *testing.T) {
	writable := t.TempDir()
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl -T",
		Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:     []string{"PATH=" + safePath() + string(os.PathListSeparator) + writable},
	}
	rep := CheckLaunch(in)
	if !hasCode(rep, ErrPathWritableDir) {
		t.Fatalf("期望违规码 %s（可写目录 %s），实际: %+v",
			ErrPathWritableDir, writable, rep.Violations)
	}
}

// TestDangerousShellEnv 验证 IFS 非空、ENV、BASH_ENV 均被拒绝。
func TestDangerousShellEnv(t *testing.T) {
	cases := []struct {
		code  string
		entry string
	}{
		{ErrIFSNonEmpty, "IFS=:"},
		{ErrIFSNonEmpty, "IFS= \t"},
		{ErrEnvSet, "ENV=/tmp/evilrc"},
		{ErrBashEnvSet, "BASH_ENV=/tmp/bashevil"},
	}
	for _, tc := range cases {
		in := LaunchInput{
			Shebang: "#!/usr/bin/perl -T",
			Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
			Env:     []string{"PATH=" + safePath(), tc.entry},
		}
		rep := CheckLaunch(in)
		if !hasCode(rep, tc.code) {
			t.Fatalf("环境 %q 期望违规码 %s，实际: %+v",
				tc.entry, tc.code, rep.Violations)
		}
	}
}

// TestEmptyIFSAllowed 验证 IFS 为空时不触发 IFS 违规。
func TestEmptyIFSAllowed(t *testing.T) {
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl -T",
		Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:     []string{"PATH=" + safePath(), "IFS="},
	}
	rep := CheckLaunch(in)
	if hasCode(rep, ErrIFSNonEmpty) {
		t.Fatalf("空 IFS 不应触发违规: %+v", rep.Violations)
	}
}

// TestRelativeInclude 验证 -I 相对路径在 shebang 与 argv 中均被拒绝。
func TestRelativeInclude(t *testing.T) {
	cases := []struct {
		name    string
		shebang string
		argv    []string
	}{
		{
			name:    "argv_relative",
			shebang: "#!/usr/bin/perl -T",
			argv:    []string{"/usr/bin/perl", "-T", "-Ilib", "upload.pl"},
		},
		{
			name:    "argv_relative_dot",
			shebang: "#!/usr/bin/perl -T",
			argv:    []string{"/usr/bin/perl", "-T", "-I./lib", "upload.pl"},
		},
		{
			name:    "shebang_relative",
			shebang: "#!/usr/bin/perl -T -Ilib",
			argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := LaunchInput{
				Shebang: tc.shebang,
				Argv:    tc.argv,
				Env:     []string{"PATH=" + safePath()},
			}
			rep := CheckLaunch(in)
			if !hasCode(rep, ErrRelativeInclude) {
				t.Fatalf("期望违规码 %s，实际: %+v",
					ErrRelativeInclude, rep.Violations)
			}
		})
	}
}

// TestAbsoluteIncludeAllowed 验证绝对路径 -I 可通过。
func TestAbsoluteIncludeAllowed(t *testing.T) {
	absLib := filepath.Join(t.TempDir(), "lib")
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl -T",
		Argv:    []string{"/usr/bin/perl", "-T", "-I" + absLib, "upload.pl"},
		Env:     []string{"PATH=" + safePath()},
	}
	rep := CheckLaunch(in)
	if hasCode(rep, ErrRelativeInclude) {
		t.Fatalf("绝对路径 -I 不应触发违规: %+v", rep.Violations)
	}
}

// TestSetuidRequiresTaint 验证 setuid 运行缺少 -T 时拒绝。
func TestSetuidRequiresTaint(t *testing.T) {
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl",
		Argv:    []string{"/usr/bin/perl", "upload.pl"},
		Env:     []string{"PATH=" + safePath()},
		Setuid:  true,
	}
	rep := CheckLaunch(in)
	if !hasCode(rep, ErrSetuidNoTaint) {
		t.Fatalf("期望违规码 %s，实际: %+v", ErrSetuidNoTaint, rep.Violations)
	}
}

// TestCompliantLaunch 验证完全合规的启动通过检查。
func TestCompliantLaunch(t *testing.T) {
	absLib := filepath.Join(t.TempDir(), "lib")
	in := LaunchInput{
		Shebang: "#!/usr/bin/perl -Tw -I" + absLib,
		Argv:    []string{"/usr/bin/perl", "-Tw", "-I" + absLib, "upload.pl"},
		Env:     []string{"PATH=" + safePath(), "HOME=/root", "LANG=C"},
	}
	rep := CheckLaunch(in)
	if !rep.OK {
		t.Fatalf("合规启动应通过，实际违规: %+v", rep.Violations)
	}
	if !slices.Equal(rep.CleanedEnv, in.Env) {
		t.Fatalf("无清理项时环境应保持不变，期望 %v，实际 %v",
			in.Env, rep.CleanedEnv)
	}
}

// TestCompliantLaunchEnvPerl 验证 env perl 形式 shebang 的合规启动。
func TestCompliantLaunchEnvPerl(t *testing.T) {
	in := LaunchInput{
		Shebang: "#!/usr/bin/env perl -T",
		Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:     []string{"PATH=" + safePath()},
	}
	rep := CheckLaunch(in)
	if !rep.OK {
		t.Fatalf("env perl 合规启动应通过，实际: %+v", rep.Violations)
	}
}

// TestNonPerlShebang 验证非 perl 首行被拒绝。
func TestNonPerlShebang(t *testing.T) {
	in := LaunchInput{
		Shebang: "#!/bin/sh",
		Argv:    []string{"/usr/bin/perl", "-T", "upload.pl"},
		Env:     []string{"PATH=" + safePath()},
	}
	rep := CheckLaunch(in)
	if !hasCode(rep, ErrShebangNotPerl) {
		t.Fatalf("期望违规码 %s，实际: %+v", ErrShebangNotPerl, rep.Violations)
	}
}
