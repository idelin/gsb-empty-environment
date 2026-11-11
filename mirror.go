package symbolcache

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// Mirror is a source of PDB bytes. A crash service typically configures one
// Mirror per upstream symbol server image.
type Mirror interface {
	// FetchPDB streams the object for idx (a symbol-server relative path) into
	// w. It must stop promptly when ctx is canceled. A partial write followed
	// by an error is expected from lossy or interrupted mirrors.
	FetchPDB(ctx context.Context, idx string, w io.Writer) error
}

// HTTPMirror fetches PDB objects from an upstream symbol server over HTTP(S).
type HTTPMirror struct {
	// BaseURL is the symbol server root, e.g. "https://symbols.example.com".
	BaseURL string
	// Client defaults to http.DefaultClient when nil.
	Client *http.Client
}

// FetchPDB implements Mirror by GET-ing "<BaseURL>/<idx>" and streaming the
// response body into w. A non-2xx status, aborted body, or short write is
// reported as an error, leaving whatever partial bytes were written behind for
// the caller to discard.
func (m *HTTPMirror) FetchPDB(ctx context.Context, idx string, w io.Writer) error {
	endpoint, err := url.JoinPath(m.BaseURL, idx)
	if err != nil {
		return fmt.Errorf("building URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mirror %s returned %s", endpoint, resp.Status)
	}

	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("streaming %s: %w", endpoint, err)
	}
	return nil
}

// Compile-time assertion that HTTPMirror is a Mirror and *os.File satisfies the
// writer shape used during downloads.
var (
	_ Mirror      = (*HTTPMirror)(nil)
	_ io.ReaderAt = (*os.File)(nil)
	_ io.Writer   = (*os.File)(nil)
)
