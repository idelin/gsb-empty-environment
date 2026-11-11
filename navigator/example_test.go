package navigator_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"example.org/offlinecli/navigator"
)

// memoryHandler is a tiny RoundTripper used to keep the example
// self-contained without opening a socket.
type memoryHandler struct{ handler http.Handler }

func (m memoryHandler) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func Example() {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(rw http.ResponseWriter, _ *http.Request) {
		http.SetCookie(rw, &http.Cookie{
			Name: "session", Value: "TOP-SECRET",
			SameSite: http.SameSiteLaxMode, Secure: true, Path: "/",
		})
		rw.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/transfer", func(rw http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(rw, "%s cookie=%q", req.Method, req.Header.Get("Cookie"))
	})

	nav := navigator.New(navigator.NewJar(),
		navigator.WithTransport(memoryHandler{mux}),
		navigator.WithLaxPostWindow(0))

	bank, _ := url.Parse("https://bank.test")
	login, _ := http.NewRequest(http.MethodGet, "https://bank.test/login", nil)
	_, _ = nav.Navigate(context.Background(),
		navigator.NewTopLevelContext(bank), login)

	// A cross-site embedded component POSTs into the bank. The Lax login
	// cookie must not be attached even though the jar holds it.
	widget, _ := url.Parse("https://widget.test")
	post, _ := http.NewRequest(http.MethodPost, "https://bank.test/transfer",
		strings.NewReader("amount=10"))
	trace, err := nav.Navigate(context.Background(),
		navigator.NewEmbeddedContext(widget), post)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	body, _ := io.ReadAll(trace.Final.Body)
	fmt.Println(string(body))
	fmt.Println("cross-site:", trace.Hops[0].CrossSite, "top-level:", trace.Hops[0].TopLevel)
	// Output:
	// POST cookie=""
	// cross-site: true top-level: false
}
