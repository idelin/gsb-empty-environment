package netsec

import (
	"encoding/xml"
	"fmt"
	"net/http"
)

// Multistatus 是 DAV:multistatus 响应体（RFC 4918 第 13 节）。
type Multistatus struct {
	XMLName  xml.Name       `xml:"DAV: multistatus"`
	Response []itemResponse `xml:"response"`
}

type itemResponse struct {
	Href   string `xml:"href"`
	Status string `xml:"status"`
	Error  string `xml:"error,omitempty"`
}

// MarshalMultistatus 把逐项结果渲染为 207 多状态 XML。
// 仅在 Result.Status 为 207（或处理器希望附带明细）时使用。
func (r *Result) MarshalMultistatus() ([]byte, error) {
	var ms Multistatus
	for _, it := range r.sortedItems() {
		ms.Response = append(ms.Response, itemResponse{
			Href:   it.Href,
			Status: fmt.Sprintf("HTTP/1.1 %d %s", it.Status, http.StatusText(it.Status)),
			Error:  it.Err,
		})
	}
	body, err := xml.MarshalIndent(ms, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}
