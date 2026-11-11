package sitemapdisc

import (
	"encoding/xml"
	"strings"
	"time"
)

type docKind int

const (
	kindUnknown docKind = iota
	kindIndex
	kindURLSet
)

type sitemapItem struct {
	Loc     string
	LastMod string
}

type sitemapDoc struct {
	kind  docKind
	items []sitemapItem
}

// xmlURLSet mirrors the <urlset> root of a sitemap file.
type xmlURLSet struct {
	XMLName xml.Name   `xml:"urlset"`
	URLs    []xmlEntry `xml:"url"`
}

// xmlIndex mirrors the <sitemapindex> root of a sitemap index file.
type xmlIndex struct {
	XMLName  xml.Name   `xml:"sitemapindex"`
	Sitemaps []xmlEntry `xml:"sitemap"`
}

type xmlEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

// parseDoc parses one sitemap document. Namespace declarations are irrelevant:
// only the local element names (urlset, sitemapindex, url, sitemap, loc,
// lastmod) are matched.
func parseDoc(data []byte) (*sitemapDoc, error) {
	var probe struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(data, &probe); err != nil {
		return nil, err
	}

	switch probe.XMLName.Local {
	case "urlset":
		var set xmlURLSet
		if err := xml.Unmarshal(data, &set); err != nil {
			return nil, err
		}
		return buildDoc(kindURLSet, set.URLs), nil
	case "sitemapindex":
		var idx xmlIndex
		if err := xml.Unmarshal(data, &idx); err != nil {
			return nil, err
		}
		return buildDoc(kindIndex, idx.Sitemaps), nil
	default:
		return nil, ErrInvalidSitemap
	}
}

func buildDoc(kind docKind, raw []xmlEntry) *sitemapDoc {
	doc := &sitemapDoc{kind: kind, items: make([]sitemapItem, 0, len(raw))}
	for _, entry := range raw {
		loc := trimSpace(entry.Loc)
		if loc == "" {
			continue
		}
		doc.items = append(doc.items, sitemapItem{
			Loc:     loc,
			LastMod: trimSpace(entry.LastMod),
		})
	}
	return doc
}

// lastModLayouts covers the W3C Datetime formats permitted by the sitemaps
// protocol, from full timestamps with offsets down to bare years.
var lastModLayouts = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04Z",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006-01",
	"2006",
}

// parseLastMod accepts every layout in lastModLayouts and returns the zero
// time for empty or unrecognized values.
func parseLastMod(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range lastModLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
