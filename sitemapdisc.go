// Package sitemapdisc discovers page URLs from sitemap indexes and urlsets.
//
// It recursively follows nested sitemap indexes, transparently decodes
// gzip-compressed payloads (detected via the gzip magic bytes rather than the
// file name), enforces per-file entry and decompressed-size limits, detects
// reference cycles, deduplicates URLs across files and only accepts references
// that share the host of their parent sitemap and live underneath its
// directory.
package sitemapdisc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultMaxEntries is the maximum number of entries allowed in a single
	// sitemap file, whether it is a urlset or a sitemap index.
	DefaultMaxEntries = 50000

	// DefaultMaxDecompressed is the maximum accepted decompressed size of a
	// single sitemap file: 50 MiB.
	DefaultMaxDecompressed int64 = 50 << 20
)

// Sentinel errors returned by Discover. They may be wrapped, so callers should
// use errors.Is rather than direct equality.
var (
	ErrTooManyEntries = errors.New("sitemapdisc: sitemap contains more entries than allowed")
	ErrTooLarge       = errors.New("sitemapdisc: sitemap exceeds the decompressed size limit")
	ErrInvalidSeed    = errors.New("sitemapdisc: seed sitemap URL is invalid")
	ErrInvalidSitemap = errors.New("sitemapdisc: malformed sitemap")
)

// Entry is a single page URL discovered in a urlset.
type Entry struct {
	// Loc is the absolute, normalized page URL.
	Loc string
	// LastMod is the parsed lastmod value. It is the zero time when lastmod is
	// absent or uses an unrecognized format; LastModRaw keeps the original.
	LastMod time.Time
	// LastModRaw is the raw lastmod string found in the sitemap.
	LastModRaw string
}

// Discoverer recursively fetches sitemap indexes and urlsets. The zero value
// is not usable; create one with New.
type Discoverer struct {
	// Client is used for all HTTP requests. Defaults to http.DefaultClient.
	Client *http.Client
	// MaxEntries bounds the number of entries in a single file. Defaults to
	// DefaultMaxEntries (50000).
	MaxEntries int
	// MaxDecompressed bounds the decompressed size of a single file. Defaults
	// to DefaultMaxDecompressed (50 MiB).
	MaxDecompressed int64
	// UserAgent is sent on every request when non-empty.
	UserAgent string
}

// New returns a Discoverer with the default limits.
func New() *Discoverer {
	return &Discoverer{
		Client:          http.DefaultClient,
		MaxEntries:      DefaultMaxEntries,
		MaxDecompressed: DefaultMaxDecompressed,
	}
}

func (d *Discoverer) applyDefaults() {
	if d.Client == nil {
		d.Client = http.DefaultClient
	}
	if d.MaxEntries <= 0 {
		d.MaxEntries = DefaultMaxEntries
	}
	if d.MaxDecompressed <= 0 {
		d.MaxDecompressed = DefaultMaxDecompressed
	}
}

// Discover starts at seedURL and walks every reachable, in-scope sitemap.
// Cycles are skipped rather than reported as errors, and page URLs seen in
// multiple files are returned once. Any fetch, parse or limit failure aborts
// the walk and returns the corresponding error.
func (d *Discoverer) Discover(ctx context.Context, seedURL string) ([]Entry, error) {
	d.applyDefaults()

	root, err := url.Parse(strings.TrimSpace(seedURL))
	if err != nil || !root.IsAbs() || !isHTTP(root) || root.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSeed, seedURL)
	}

	visitedSitemaps := make(map[string]struct{})
	seenPages := make(map[string]struct{})
	var entries []Entry
	queue := []*url.URL{root}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		key := current.String()
		if _, ok := visitedSitemaps[key]; ok {
			continue
		}
		visitedSitemaps[key] = struct{}{}

		doc, effective, err := d.fetch(ctx, current)
		if err != nil {
			return nil, err
		}
		// The final URL after redirects is part of the cycle set too.
		visitedSitemaps[effective.String()] = struct{}{}

		if len(doc.items) > d.MaxEntries {
			return nil, fmt.Errorf("%w: %s lists %d entries (limit %d)",
				ErrTooManyEntries, effective.String(), len(doc.items), d.MaxEntries)
		}

		switch doc.kind {
		case kindIndex:
			for _, item := range doc.items {
				child, ok := resolveChild(effective, item.Loc)
				if !ok {
					continue
				}
				if _, ok := visitedSitemaps[child.String()]; !ok {
					queue = append(queue, child)
				}
			}
		case kindURLSet:
			for _, item := range doc.items {
				child, ok := resolveChild(effective, item.Loc)
				if !ok {
					continue
				}
				loc := child.String()
				if _, ok := seenPages[loc]; ok {
					continue
				}
				seenPages[loc] = struct{}{}
				entries = append(entries, Entry{
					Loc:        loc,
					LastMod:    parseLastMod(item.LastMod),
					LastModRaw: item.LastMod,
				})
			}
		default:
			return nil, fmt.Errorf("%w: %s has an unknown root element", ErrInvalidSitemap, effective.String())
		}
	}

	return entries, nil
}

// fetch downloads one sitemap, transparently inflating gzip payloads, and
// parses it. It returns the effective request URL, which may differ from the
// requested one when the client followed redirects.
func (d *Discoverer) fetch(ctx context.Context, u *url.URL) (*sitemapDoc, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	if d.UserAgent != "" {
		req.Header.Set("User-Agent", d.UserAgent)
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("sitemapdisc: fetch %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("sitemapdisc: fetch %s: unexpected status %s", u.String(), resp.Status)
	}

	// One byte beyond the limit so crossing the boundary is detectable.
	capBytes := d.MaxDecompressed + 1
	raw, err := readCapped(resp.Body, capBytes)
	if err != nil {
		return nil, nil, err
	}

	data := raw
	if isGzip(raw) {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, nil, fmt.Errorf("sitemapdisc: gzip decode %s: %w", u.String(), err)
		}
		data, err = readCapped(gz, capBytes)
		if err != nil {
			return nil, nil, err
		}
		// A stream cut off by the size limit reports ErrUnexpectedEOF on
		// close; genuine integrity errors still surface on the next read.
		_ = gz.Close()
	}

	doc, err := parseDoc(data)
	if err != nil {
		return nil, nil, fmt.Errorf("sitemapdisc: parse %s: %w", u.String(), err)
	}
	return doc, resp.Request.URL, nil
}

// readCapped reads at most capBytes and returns ErrTooLarge when the cap is
// reached, meaning the stream contained at least capBytes bytes.
func readCapped(r io.Reader, capBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, capBytes))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) >= capBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

func isGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func isHTTP(u *url.URL) bool {
	return u.Scheme == "http" || u.Scheme == "https"
}
