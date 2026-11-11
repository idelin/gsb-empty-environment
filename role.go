package rpalocate

import "strings"

// roleOf 返回元素的 ARIA 角色：显式 role 属性优先，否则按标签推断隐式角色。
func roleOf(e *Element) string {
	if r, ok := e.Attr("role"); ok && normalize(r) != "" {
		return normalize(r)
	}
	switch e.Tag {
	case "button":
		return "button"
	case "a":
		if _, ok := e.Attr("href"); ok {
			return "link"
		}
	case "input":
		t, _ := e.Attr("type")
		switch strings.ToLower(t) {
		case "submit", "button", "reset", "image":
			return "button"
		case "checkbox":
			return "checkbox"
		case "radio":
			return "radio"
		case "range":
			return "slider"
		default:
			return "textbox"
		}
	case "select":
		return "combobox"
	case "textarea":
		return "textbox"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "heading"
	case "nav":
		return "navigation"
	case "main":
		return "main"
	case "ul", "ol":
		return "list"
	case "li":
		return "listitem"
	case "table":
		return "table"
	case "img":
		return "img"
	}
	return ""
}

// actionable 判断元素是否是 RPA 可交互（可点击）的候选。
func actionable(e *Element) bool {
	switch e.Tag {
	case "button", "a", "input", "select", "textarea", "summary", "option":
		return true
	}
	if _, ok := e.Attr("onclick"); ok {
		return true
	}
	if _, ok := e.Attr("tabindex"); ok {
		return true
	}
	switch roleOf(e) {
	case "button", "link", "checkbox", "radio", "menuitem", "tab", "switch":
		return true
	}
	return false
}
