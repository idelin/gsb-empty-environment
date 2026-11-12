package redirect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func do(t *testing.T, c *Client, req *http.Request) *http.Response {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPost302RewritesToGetAndDropsBody(t *testing.T) {
	net := newLocalNet()
	mux := http.NewServeMux()
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("hop 1 method = %s, want POST", r.Method)
		}
		http.Redirect(w, r, "/landing", http.StatusFound)
	})
	mux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("hop 2 method = %s, want GET", r.Method)
		}
		if r.ContentLength != 0 {
			t.Errorf("hop 2 ContentLength = %d, want 0", r.ContentLength)
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			t.Errorf("hop 2 Content-Type = %q, want empty", ct)
		}
		if te := r.Header.Get("Transfer-Encoding"); te != "" {
			t.Errorf("hop 2 Transfer-Encoding = %q, want empty", te)
		}
		if body, _ := io.ReadAll(r.Body); len(body) != 0 {
			t.Errorf("hop 2 body = %q, want empty", body)
		}
	})
	base := net.Serve(t, "app.local", mux)

	req, err := http.NewRequest(http.MethodPost, base+"/submit", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp := do(t, &Client{Transport: net.Transport}, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPost307ReplaysBody(t *testing.T) {
	net := newLocalNet()
	var hops atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hop := hops.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("hop %d body = %q, want payload", hop, body)
		}
		if r.Method != http.MethodPost {
			t.Errorf("hop %d method = %s, want POST", hop, r.Method)
		}
		if hop == 1 {
			http.Redirect(w, r, "/again", http.StatusTemporaryRedirect)
		}
	})
	base := net.Serve(t, "app.local", mux)

	req, err := http.NewRequest(http.MethodPost, base+"/", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	do(t, &Client{Transport: net.Transport}, req)
	if got := hops.Load(); got != 2 {
		t.Fatalf("hops = %d, want 2", got)
	}
}

func TestPut303RewritesToGet(t *testing.T) {
	net := newLocalNet()
	mux := http.NewServeMux()
	mux.HandleFunc("/res", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/res/view", http.StatusSeeOther)
	})
	mux.HandleFunc("/res/view", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
	})
	base := net.Serve(t, "app.local", mux)

	req, err := http.NewRequest(http.MethodPut, base+"/res", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	do(t, &Client{Transport: net.Transport}, req)
}

func TestSensitiveHeadersReauthorizedByOrigin(t *testing.T) {
	net := newLocalNet()
	var crossAuth, crossCookie atomic.Value
	crossBase := net.Serve(t, "other.local", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		crossAuth.Store(r.Header.Get("Authorization"))
		crossCookie.Store(r.Header.Get("Cookie"))
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/cross", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, crossBase+"/final", http.StatusFound)
	})
	mux.HandleFunc("/same", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("same-origin Authorization = %q, want kept", got)
		}
	})
	base := net.Serve(t, "origin.local", mux)

	newReq := func(path string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Cookie", "session=abc")
		return req
	}
	client := &Client{Transport: net.Transport}

	do(t, client, newReq("/cross"))
	if got := crossAuth.Load(); got != "" {
		t.Errorf("cross-origin Authorization = %q, want stripped", got)
	}
	if got := crossCookie.Load(); got != "" {
		t.Errorf("cross-origin Cookie = %q, want stripped", got)
	}

	do(t, client, newReq("/same"))
}

func TestLoopDetected(t *testing.T) {
	net := newLocalNet()
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/a", http.StatusFound)
	})
	base := net.Serve(t, "app.local", mux)

	req, err := http.NewRequest(http.MethodGet, base+"/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Client{Transport: net.Transport}).Do(req)
	if !errors.Is(err, ErrLoop) {
		t.Fatalf("err = %v, want ErrLoop", err)
	}
}

func TestTooManyHops(t *testing.T) {
	net := newLocalNet()
	base := net.Serve(t, "app.local", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscanf(r.URL.Query().Get("n"), "%d", &n)
		http.Redirect(w, r, fmt.Sprintf("/hop?n=%d", n+1), http.StatusFound)
	}))

	req, err := http.NewRequest(http.MethodGet, base+"/hop?n=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Client{Transport: net.Transport, Planner: &Planner{MaxHops: 3}}).Do(req)
	if !errors.Is(err, ErrTooManyHops) {
		t.Fatalf("err = %v, want ErrTooManyHops", err)
	}
}

func TestCancelDoesNotResendCompletedHop(t *testing.T) {
	net := newLocalNet()
	received := make(chan struct{})
	proceed := make(chan struct{})
	var starts, finals atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		starts.Add(1)
		close(received)
		<-proceed
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		finals.Add(1)
	})
	base := net.Serve(t, "app.local", mux)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, err := (&Client{Transport: net.Transport}).Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()

	<-received
	cancel()
	close(proceed)

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("/start hits = %d, want 1 (completed hop not resent)", got)
	}
	if got := finals.Load(); got != 0 {
		t.Errorf("/final hits = %d, want 0 after cancel", got)
	}
}

func TestBodyWithoutGetBodyCannotReplay(t *testing.T) {
	net := newLocalNet()
	base := net.Serve(t, "app.local", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusTemporaryRedirect)
	}))

	req, err := http.NewRequest(http.MethodPost, base+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = io.NopCloser(strings.NewReader("payload"))
	req.GetBody = nil
	req.ContentLength = 7

	_, err = (&Client{Transport: net.Transport}).Do(req)
	if !errors.Is(err, ErrBodyReplay) {
		t.Fatalf("err = %v, want ErrBodyReplay", err)
	}
}

func TestPlannerMethodPolicy(t *testing.T) {
	cases := []struct {
		status int
		method string
		want   string
	}{
		{301, "POST", "GET"},
		{301, "GET", "GET"},
		{302, "POST", "GET"},
		{302, "HEAD", "HEAD"},
		{303, "PUT", "GET"},
		{303, "HEAD", "HEAD"},
		{307, "POST", "POST"},
		{308, "DELETE", "DELETE"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.method), func(t *testing.T) {
			req, err := http.NewRequest(tc.method, "http://example.com/a", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp := &http.Response{
				StatusCode: tc.status,
				Header:     http.Header{"Location": []string{"/b"}},
			}
			next, err := (&Planner{}).Next(req, resp, nil)
			if err != nil {
				t.Fatal(err)
			}
			if next.Method != tc.want {
				t.Errorf("method = %s, want %s", next.Method, tc.want)
			}
		})
	}
}
