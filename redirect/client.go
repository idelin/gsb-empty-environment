package redirect

import (
	"io"
	"net/http"
)

// Client issues requests through a RoundTripper and follows redirects
// according to a Planner.
type Client struct {
	// Transport performs each hop. Nil uses http.DefaultTransport.
	Transport http.RoundTripper
	// Planner plans each follow-up. Nil uses a zero Planner.
	Planner *Planner
}

func (c *Client) transport() http.RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	return http.DefaultTransport
}

func (c *Client) planner() *Planner {
	if c.Planner != nil {
		return c.Planner
	}
	return &Planner{}
}

// Do sends req and follows redirects until a non-redirect response, a
// planning error, or context cancellation. The context is checked before
// every hop, so a cancelled chain never re-sends a hop that already
// completed.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	tr := c.transport()
	planner := c.planner()
	var via []*http.Request
	for {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		resp, err := tr.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		next, err := planner.Next(req, resp, via)
		if err != nil {
			resp.Body.Close()
			return nil, err
		}
		if next == nil {
			return resp, nil
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		via = append(via, req)
		req = next
	}
}
