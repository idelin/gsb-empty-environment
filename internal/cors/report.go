package cors

import (
	"fmt"
	"io"
	"strings"
)

// RenderText 输出人类可读的复盘报告。
func RenderText(out io.Writer, result *RunResult) {
	for i, r := range result.Requests {
		if i > 0 {
			fmt.Fprintln(out, strings.Repeat("-", 78))
		}
		fmt.Fprintf(out, "[%s] t=%ds  %s %s\n", r.ID, r.AtSeconds, r.Method, r.URL)
		fmt.Fprintf(out, "  发起源: %s    凭据: %s\n", r.Source, credLabel(r.Credentials))
		if r.Note != "" {
			fmt.Fprintf(out, "  说明: %s\n", r.Note)
		}
		fmt.Fprintf(out, "  请求能否发出: %s    脚本能否读回: %s    结论: %s\n",
			yesNo(r.RequestSent), yesNo(r.ScriptReadable), statusLabel(r.Status))
		if r.FinalURL != "" {
			fmt.Fprintf(out, "  最终 URL: %s (HTTP %d)\n", r.FinalURL, r.FinalStatus)
		}
		for _, reason := range r.Reasons {
			fmt.Fprintf(out, "    - %s\n", reason)
		}
		for _, hop := range r.Hops {
			renderHop(out, hop)
		}
	}

	fmt.Fprintln(out, strings.Repeat("=", 78))
	fmt.Fprintln(out, "预检缓存终态（按 发起源 / 目标URL / 凭据条件 隔离）")
	if len(result.Cache) == 0 {
		fmt.Fprintln(out, "  (空)")
		return
	}
	for _, row := range result.Cache {
		expiry := "会话级"
		if row.ExpiresAt >= 0 {
			expiry = fmt.Sprintf("t=%d 过期", row.ExpiresAt)
		}
		state := "有效"
		if !row.Live {
			state = "已过期"
		}
		fmt.Fprintf(out, "  [%s] %s -> %s  cred=%s\n",
			state, row.Source, row.Target, credLabel(row.Credentials))
		fmt.Fprintf(out, "      方法: %s | 头: %s | 写入 t=%d | %s\n",
			joinOrAny(row.Methods), joinOrAny(row.Headers), row.CachedAt, expiry)
	}
}

func renderHop(out io.Writer, hop HopDetail) {
	scope := "跨源"
	if !hop.CrossOrigin {
		scope = "同源"
	}
	fmt.Fprintf(out, "  第 %d 跳 [%s] %s %s\n", hop.Hop, scope, hop.Method, hop.Target)
	if hop.Preflight != nil {
		pf := hop.Preflight
		switch {
		case pf.CacheHit:
			fmt.Fprintln(out, "    预检: 缓存命中（不发起 OPTIONS）")
		case pf.OK:
			fmt.Fprintf(out, "    预检: OPTIONS HTTP %d 通过，缓存 Max-Age=%ds\n",
				pf.Status, pf.MaxAgeSeconds)
		default:
			fmt.Fprintf(out, "    预检: OPTIONS HTTP %d 失败 → 实际请求被拦截\n", pf.Status)
		}
		for _, reason := range pf.FailureReasons {
			fmt.Fprintf(out, "      - %s\n", reason)
		}
	}
	if hop.RedirectLocation != "" {
		fmt.Fprintf(out, "    实际响应: HTTP %d → 跳转 %s\n",
			hop.HTTPStatus, hop.RedirectLocation)
	}
	if hop.Actual != nil {
		act := hop.Actual
		if act.Readable {
			fmt.Fprintf(out, "    实际响应: HTTP %d CORS 检查通过，可读头: %s\n",
				act.Status, joinOrAny(act.ExposedHeaders))
		} else {
			fmt.Fprintf(out, "    实际响应: HTTP %d CORS 检查失败（响应不透明，请求确实已发出）\n",
				act.Status)
			for _, reason := range act.FailureReasons {
				fmt.Fprintf(out, "      - %s\n", reason)
			}
		}
	}
}

func credLabel(credentials bool) string {
	if credentials {
		return "include"
	}
	return "omit"
}

func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func statusLabel(status string) string {
	switch status {
	case StatusOK:
		return "通过（可读）"
	case StatusBlocked:
		return "预检拦截（未发出）"
	case StatusOpaque:
		return "响应不透明（已发出但不可读）"
	case StatusSimulationError:
		return "建模错误"
	default:
		return status
	}
}

func joinOrAny(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
