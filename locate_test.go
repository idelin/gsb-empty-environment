package rpalocate

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *Element {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	root, err := ParseHTML(string(data))
	if err != nil {
		t.Fatalf("解析快照 %s 失败: %v", name, err)
	}
	return root
}

func byID(t *testing.T, root *Element, id string) *Element {
	t.Helper()
	for _, el := range root.Elements() {
		if v, ok := el.Attr("id"); ok && v == id {
			return el
		}
	}
	t.Fatalf("快照中找不到 id=%q 的元素", id)
	return nil
}

// 录制后在同一页面回放，应以满分定位到原元素。
func TestRecordReplaySamePage(t *testing.T) {
	before := load(t, "page_before.html")
	target := byID(t, before, "delete-1024")
	loc := Record(target)

	if loc.Text != "删除订单" {
		t.Fatalf("录制文本 = %q，期望 %q", loc.Text, "删除订单")
	}
	if loc.Role != "button" {
		t.Fatalf("录制角色 = %q，期望 button", loc.Role)
	}
	if len(loc.Neighbors) == 0 {
		t.Fatal("录制结果缺少邻居锚点")
	}

	got, err := Locate(before, loc)
	if err != nil {
		t.Fatalf("同页回放失败: %v", err)
	}
	if got.Element != target {
		t.Fatalf("同页回放定位到 %s，期望原元素", got.Element.Path())
	}
	if got.Score < 0.999 {
		t.Fatalf("同页回放得分 = %.3f，期望约 1.0", got.Score)
	}
}

// 改版后：按钮变成 <a role="button">、id 消失、布局移动，
// 多锚点定位器仍应唯一命中订单 #1024 的“删除订单”。
func TestLocateAfterRedesign(t *testing.T) {
	before := load(t, "page_before.html")
	after := load(t, "page_after.html")
	loc := Record(byID(t, before, "delete-1024"))

	got, err := Locate(after, loc)
	if err != nil {
		t.Fatalf("改版后定位失败: %v", err)
	}
	href, _ := got.Element.Attr("href")
	if got.Element.Tag != "a" || href != "/orders/1024/delete" {
		t.Fatalf("定位到错误元素: <%s> href=%q 文本=%q",
			got.Element.Tag, href, got.Element.TextContent())
	}
	if got.Element.TextContent() != "删除订单" {
		t.Fatalf("命中元素文本 = %q，期望 %q", got.Element.TextContent(), "删除订单")
	}

	// 匹配理由必须可解释：说明哪些锚点命中、哪些未命中。
	joined := strings.Join(got.Reasons, "\n")
	t.Logf("命中元素 %s，得分 %.3f，理由：\n%s", got.Element.Path(), got.Score, joined)
	for _, want := range []string{"文本完全匹配", "角色", "邻居[section-heading]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("匹配理由缺少 %q", want)
		}
	}
}

// 旧版单 XPath 定位器在改版后要么失效、要么静默点错按钮。
func TestLegacyXPathBreaksOrMisclicks(t *testing.T) {
	before := load(t, "page_before.html")
	after := load(t, "page_after.html")
	loc := Record(byID(t, before, "delete-1024"))

	// 情形一：录制的绝对路径在改版后直接失效。
	if _, err := FindByXPath(after, loc.XPath); err == nil {
		t.Fatalf("绝对路径 %q 在改版后不应再匹配", loc.XPath)
	}

	// 情形二：位置型 XPath //button[2] 改版前指向“删除订单”，
	// 改版后静默指向危险的“清空所有订单”——即误点事故。
	legacy := "//button[2]"
	elBefore, err := FindByXPath(before, legacy)
	if err != nil {
		t.Fatalf("改版前 %q 未匹配: %v", legacy, err)
	}
	if id, _ := elBefore.Attr("id"); id != "delete-1024" {
		t.Fatalf("改版前 %q 指向 id=%q，期望 delete-1024", legacy, id)
	}
	elAfter, err := FindByXPath(after, legacy)
	if err != nil {
		t.Fatalf("改版后 %q 未匹配: %v", legacy, err)
	}
	if text := elAfter.TextContent(); text != "清空所有订单" {
		t.Fatalf("改版后 %q 指向 %q，期望演示其指向“清空所有订单”", legacy, text)
	}

	// 多锚点定位器不会跟随 XPath 误点：它命中真正的“删除订单”。
	got, err := Locate(after, loc)
	if err != nil {
		t.Fatalf("多锚点定位失败: %v", err)
	}
	if got.Element == elAfter {
		t.Fatal("多锚点定位器跟随 XPath 误点了“清空所有订单”")
	}
}

// 两个一模一样的“删除订单”按钮：只凭文本定位时最高分不唯一，
// 必须报告歧义并拒绝点击。
func TestAmbiguityRefusesClick(t *testing.T) {
	before := load(t, "page_before.html")
	loc := Locator{Text: "删除订单", Tag: "button"}

	_, err := Locate(before, loc)
	var amb *AmbiguityError
	if !errors.As(err, &amb) {
		t.Fatalf("期望 AmbiguityError，得到 %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("歧义候选数 = %d，期望 2", len(amb.Candidates))
	}
	for _, c := range amb.Candidates {
		id, _ := c.Element.Attr("id")
		t.Logf("歧义候选 id=%s 得分 %.3f", id, c.Score)
	}
}

// 目标在改版后被移除：最高分低于阈值，必须拒绝点击而不是
// 勉强点击最接近的元素。
func TestLowConfidenceRefusesClick(t *testing.T) {
	after := load(t, "page_after.html")
	loc := Locator{
		Tag:   "button",
		Text:  "导出报表",
		Attrs: []Attr{{Name: "id", Value: "export"}},
	}

	_, err := Locate(after, loc)
	var low *LowConfidenceError
	if !errors.As(err, &low) {
		t.Fatalf("期望 LowConfidenceError，得到 %v", err)
	}
	if low.Score >= low.Threshold {
		t.Fatalf("得分 %.3f 不应达到阈值 %.3f", low.Score, low.Threshold)
	}
	t.Logf("最佳候选 %s 得分 %.3f，理由：\n%s",
		low.Best.Element.Path(), low.Score, strings.Join(low.Best.Reasons, "\n"))
}

// 改版后定位“立即支付”同样应跨标签命中，验证角色锚点让
// <button> 录制的定位器能匹配 <a role="button">。
func TestLocatePayButtonAfterRedesign(t *testing.T) {
	before := load(t, "page_before.html")
	after := load(t, "page_after.html")
	loc := Record(byID(t, before, "pay-1024"))

	got, err := Locate(after, loc)
	if err != nil {
		t.Fatalf("改版后定位失败: %v", err)
	}
	href, _ := got.Element.Attr("href")
	if href != "/orders/1024/pay" {
		t.Fatalf("定位到 href=%q，期望 /orders/1024/pay", href)
	}
}
