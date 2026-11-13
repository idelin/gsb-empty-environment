// Package netsec 提供 Perl 脚本执行前的启动安全检查。
//
// Perl 的污点模式（taint mode）只有在解释器启动时携带 -T 才会真正生效：
// 脚本首行 shebang 里的 #!/usr/bin/perl -T 与实际启动参数必须保持一致，
// 仅启用 -t（taint warnings）不等于污点模式。本包在任务启动器执行脚本前
// 校验污点模式、解释器搜索路径、危险环境变量以及 -I 相对路径等问题。
package netsec

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// EnvPolicy 决定如何处理 PERL5OPT 与 PERL5LIB。
type EnvPolicy int

const (
	// EnvReject 表示环境中存在 PERL5OPT 或 PERL5LIB 时直接拒绝启动。
	EnvReject EnvPolicy = iota
	// EnvSanitize 表示从环境中清除 PERL5OPT 与 PERL5LIB 后再启动。
	EnvSanitize
)

// LaunchInput 描述一次 Perl 脚本启动的输入。
type LaunchInput struct {
	// Shebang 是脚本首行，例如 "#!/usr/bin/perl -T"；不含换行。
	Shebang string
	// Argv 是实际启动参数列表，argv[0] 为 perl 解释器路径，
	// 例如 ["/usr/bin/perl", "-T", "upload.pl"]。
	Argv []string
	// Env 是环境变量列表，元素形如 "KEY=VALUE"。
	Env []string
	// Setuid 表示脚本是否以 setuid/setgid 方式运行。
	Setuid bool
	// PerlEnvPolicy 决定 PERL5OPT/PERL5LIB 的处理策略，零值为拒绝。
	PerlEnvPolicy EnvPolicy
}

// 违规类型常量，便于调用方按错误码做程序化判断。
const (
	// ErrShebangNotPerl：首行不是 perl 解释器。
	ErrShebangNotPerl = "shebang_not_perl"
	// ErrTaintMismatch：首行与启动参数的 -T 配置不一致。
	ErrTaintMismatch = "taint_shebang_argv_mismatch"
	// ErrTaintNotStrong：只出现 -t（taint warnings），没有 -T。
	ErrTaintNotStrong = "taint_only_t_not_T"
	// ErrTaintMissing：首行与启动参数都没有 -T，污点模式未生效。
	ErrTaintMissing = "taint_mode_not_enabled"
	// ErrSetuidNoTaint：setuid/setgid 运行但未显式启用 -T。
	ErrSetuidNoTaint = "setuid_requires_taint"
	// ErrPerl5Opt：环境含 PERL5OPT（拒绝策略下）。
	ErrPerl5Opt = "perl5opt_injection"
	// ErrPerl5Lib：环境含 PERL5LIB（拒绝策略下）。
	ErrPerl5Lib = "perl5lib_injection"
	// ErrPathCurrentDir：PATH 含当前目录（. 或空条目）。
	ErrPathCurrentDir = "path_contains_current_dir"
	// ErrPathWritableDir：PATH 含当前用户可写的目录。
	ErrPathWritableDir = "path_contains_writable_dir"
	// ErrIFSNonEmpty：IFS 被设置为非空值。
	ErrIFSNonEmpty = "ifs_nonempty"
	// ErrEnvSet：环境含 ENV（可能指向恶意初始化文件）。
	ErrEnvSet = "env_variable_set"
	// ErrBashEnvSet：环境含 BASH_ENV。
	ErrBashEnvSet = "bash_env_variable_set"
	// ErrRelativeInclude：-I 指向相对路径。
	ErrRelativeInclude = "relative_include_path"
)

// Violation 描述一条启动检查违规。
type Violation struct {
	Code    string
	Message string
}

func (v Violation) Error() string {
	return v.Code + ": " + v.Message
}

// Report 是启动检查结果。
type Report struct {
	// OK 为 true 时表示可以启动。
	OK bool
	// Violations 收集全部违规项（检查不会在首个错误处提前返回）。
	Violations []Violation
	// CleanedEnv 是 EnvSanitize 策略下清除 PERL5OPT/PERL5LIB 后的环境；
	// 拒绝策略或无需清理时与输入环境一致。
	CleanedEnv []string
}

// CheckLaunch 在执行 Perl 脚本前执行全部启动安全检查。
//
// 检查项：
//   - 首行必须是 perl 解释器；
//   - 首行与启动参数必须同时带 -T，仅 -t 不算污点模式；
//   - setuid 运行必须显式启用 -T；
//   - 按策略拒绝或清除 PERL5OPT、PERL5LIB；
//   - PATH 不得包含当前目录或当前用户可写目录；
//   - IFS 非空、存在 ENV 或 BASH_ENV 时拒绝；
//   - 任何 -I 指向相对路径时拒绝。
func CheckLaunch(in LaunchInput) Report {
	rep := &Report{CleanedEnv: append([]string(nil), in.Env...)}

	shebangFlags, shebangViolations := parseShebang(in.Shebang)
	rep.Violations = append(rep.Violations, shebangViolations...)

	argvFlags, argvViolations := parseArgv(in.Argv)
	rep.Violations = append(rep.Violations, argvViolations...)

	if len(shebangViolations) == 0 {
		rep.Violations = append(rep.Violations, checkTaint(shebangFlags, argvFlags, in.Setuid)...)
		rep.Violations = append(rep.Violations, checkIncludes(shebangFlags)...)
	}
	rep.Violations = append(rep.Violations, checkIncludes(argvFlags)...)

	rep.Violations = append(rep.Violations, checkEnv(in, rep)...)

	rep.OK = len(rep.Violations) == 0
	return *rep
}

// flagInfo 是从 shebang 或 argv 中解析出的 perl 标志信息。
type flagInfo struct {
	hasTaint     bool
	hasTaintWarn bool
	includePaths []string
}

// parseShebang 解析脚本首行，返回标志信息与违规项。
func parseShebang(line string) (flagInfo, []Violation) {
	info := flagInfo{}
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return info, []Violation{{
			Code:    ErrShebangNotPerl,
			Message: "脚本首行为空，缺少 perl shebang",
		}}
	}
	if !strings.HasPrefix(trimmed, "#!") {
		return info, []Violation{{
			Code:    ErrShebangNotPerl,
			Message: "脚本首行不是 shebang（缺少 #! 前缀）",
		}}
	}

	fields := strings.Fields(trimmed[2:])
	if len(fields) == 0 {
		return info, []Violation{{
			Code:    ErrShebangNotPerl,
			Message: "shebang 未指定解释器",
		}}
	}

	// 支持 "#!/usr/bin/env perl" 形式：跳过 env 及其选项，再定位 perl。
	start := 0
	if filepath.Base(fields[0]) == "env" {
		start = 1
		for start < len(fields) && strings.HasPrefix(fields[start], "-") {
			start++
		}
	}
	if start >= len(fields) || filepath.Base(fields[start]) != "perl" {
		return info, []Violation{{
			Code:    ErrShebangNotPerl,
			Message: "shebang 解释器不是 perl: " + fields[0],
		}}
	}

	return walkFlags(fields[start+1:]), nil
}

// parseArgv 解析实际启动参数，跳过 argv[0] 解释器与脚本名之后的内容。
func parseArgv(argv []string) (flagInfo, []Violation) {
	if len(argv) == 0 {
		return flagInfo{}, []Violation{{
			Code:    ErrShebangNotPerl,
			Message: "启动参数为空，缺少 perl 解释器",
		}}
	}
	args := argv[1:]
	flags := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		flags = append(flags, arg)
	}
	return walkFlags(flags), nil
}

// walkFlags 遍历 perl 标志，识别 -T/-t 与 -I（含 -wT、-Tw 等组合写法）。
// 组合标志中 -M/-m/-e/-E/-C/-d/-D/-x 等带值字母会吞掉其后的剩余字符。
func walkFlags(flags []string) flagInfo {
	info := flagInfo{}
	consumesRest := map[byte]bool{
		'M': true, 'm': true, 'e': true, 'E': true,
		'C': true, 'd': true, 'D': true, 'x': true,
	}

	for _, flag := range flags {
		body := strings.TrimPrefix(flag, "-")
		if body == "" {
			continue
		}

		// -I/path 形式（-I 与路径被合并在同一个参数里）。
		if body[0] == 'I' {
			if len(body) > 1 {
				info.includePaths = append(info.includePaths, body[1:])
			}
			continue
		}

	bodyChars:
		for i := 0; i < len(body); i++ {
			switch body[i] {
			case 'T':
				info.hasTaint = true
			case 't':
				info.hasTaintWarn = true
			case 'I':
				if i+1 < len(body) {
					info.includePaths = append(info.includePaths, body[i+1:])
				}
				break bodyChars
			default:
				if consumesRest[body[i]] {
					i = len(body)
				}
			}
		}
	}
	return info
}

// checkTaint 校验污点模式是否生效且首行与启动参数一致。
func checkTaint(shebang, argv flagInfo, setuid bool) []Violation {
	var violations []Violation

	if shebang.hasTaint != argv.hasTaint {
		violations = append(violations, Violation{
			Code: ErrTaintMismatch,
			Message: "首行与启动参数的 -T 不一致：shebang 带 -T=" +
				boolWord(shebang.hasTaint) + "，argv 带 -T=" + boolWord(argv.hasTaint),
		})
	}

	if !shebang.hasTaint && !argv.hasTaint {
		if shebang.hasTaintWarn || argv.hasTaintWarn {
			violations = append(violations, Violation{
				Code:    ErrTaintNotStrong,
				Message: "仅启用 -t（taint warnings）不等于污点模式，必须使用 -T",
			})
		} else {
			violations = append(violations, Violation{
				Code:    ErrTaintMissing,
				Message: "首行与启动参数均未携带 -T，污点模式未生效",
			})
		}
	}

	if setuid && !shebang.hasTaint && !argv.hasTaint {
		violations = append(violations, Violation{
			Code:    ErrSetuidNoTaint,
			Message: "setuid/setgid 运行的 perl 脚本必须显式启用 -T 污点模式",
		})
	}

	return violations
}

// checkIncludes 拒绝任何指向相对路径的 -I。
func checkIncludes(info flagInfo) []Violation {
	var violations []Violation
	for _, path := range info.includePaths {
		if path == "" || !filepath.IsAbs(path) {
			violations = append(violations, Violation{
				Code:    ErrRelativeInclude,
				Message: "-I 必须使用绝对路径，拒绝相对路径: " + path,
			})
		}
	}
	return violations
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// checkEnv 校验环境变量，并按策略清理 PERL5OPT/PERL5LIB。
func checkEnv(in LaunchInput, rep *Report) []Violation {
	var violations []Violation
	cleaned := make([]string, 0, len(in.Env))

	for _, entry := range in.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			// 无 "=" 的条目不是合法环境变量，原样保留。
			cleaned = append(cleaned, entry)
			continue
		}

		switch key {
		case "PERL5OPT":
			if in.PerlEnvPolicy == EnvSanitize {
				continue
			}
			violations = append(violations, Violation{
				Code:    ErrPerl5Opt,
				Message: "环境含 PERL5OPT，可能注入 perl 命令行选项: " + value,
			})
		case "PERL5LIB":
			if in.PerlEnvPolicy == EnvSanitize {
				continue
			}
			violations = append(violations, Violation{
				Code:    ErrPerl5Lib,
				Message: "环境含 PERL5LIB，可能注入恶意模块搜索路径: " + value,
			})
		case "IFS":
			if value != "" {
				violations = append(violations, Violation{
					Code:    ErrIFSNonEmpty,
					Message: "IFS 必须保持默认（未设置或为空），当前值: " + value,
				})
			}
		case "ENV":
			violations = append(violations, Violation{
				Code:    ErrEnvSet,
				Message: "环境含 ENV，可能加载恶意 shell 初始化文件: " + value,
			})
		case "BASH_ENV":
			violations = append(violations, Violation{
				Code:    ErrBashEnvSet,
				Message: "环境含 BASH_ENV，可能加载恶意 shell 初始化文件: " + value,
			})
		case "PATH":
			violations = append(violations, checkPath(value)...)
		}

		cleaned = append(cleaned, entry)
	}

	rep.CleanedEnv = cleaned
	return violations
}

// checkPath 校验 PATH 中每个目录：当前目录与可写目录一律拒绝。
func checkPath(value string) []Violation {
	var violations []Violation
	for _, dir := range filepath.SplitList(value) {
		if dir == "" || dir == "." {
			violations = append(violations, Violation{
				Code:    ErrPathCurrentDir,
				Message: "PATH 包含当前目录（. 或空条目）",
			})
			continue
		}
		writable, err := isWritableDir(dir)
		if err != nil {
			violations = append(violations, Violation{
				Code:    ErrPathWritableDir,
				Message: "PATH 目录无法校验权限: " + dir + " (" + err.Error() + ")",
			})
			continue
		}
		if writable {
			violations = append(violations, Violation{
				Code:    ErrPathWritableDir,
				Message: "PATH 包含当前用户可写目录: " + dir,
			})
		}
	}
	return violations
}

// isWritableDir 判断目录对当前用户是否可写，按属主/属组/其他三类权限位判断。
func isWritableDir(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// 退化为访问位检查（非 Unix 平台理论上不会走到这里）。
		return info.Mode().Perm()&0o002 != 0, nil
	}

	perm := info.Mode().Perm()
	euid := uint32(os.Geteuid())
	egid := uint32(os.Getegid())

	switch {
	case stat.Uid == euid:
		return perm&0o200 != 0, nil
	case stat.Gid == egid:
		return perm&0o020 != 0, nil
	default:
		return perm&0o002 != 0, nil
	}
}
