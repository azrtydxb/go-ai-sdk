package bfl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/fetchmedia"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateImages_CreatePollSampleHappyPath(t *testing.T) {
	sampleBytes := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")

	// newFixtureServer's /poll handler always points result.sample at this
	// same server's /sample path (see below), so pollBodies here only need
	// the status transitions; the sample URL is filled in dynamically.
	var srv *httptest.Server
	pollBodies := []string{
		`{"id":"gen-1","status":"Pending"}`,
		`{"id":"gen-1","status":"Ready","result":{"sample":"SAMPLE_URL"}}`,
	}

	mux := http.NewServeMux()
	var pollCount int32
	var gotCreateKey, gotPollKey string
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		gotCreateKey = r.Header.Get("x-key")
		io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		gotPollKey = r.Header.Get("x-key")
		n := atomic.AddInt32(&pollCount, 1)
		idx := int(n) - 1
		if idx >= len(pollBodies) {
			idx = len(pollBodies) - 1
		}
		body := strings.ReplaceAll(pollBodies[idx], "SAMPLE_URL", srv.URL+"/sample")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		Size:   "1024x768",
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotCreateKey != "test-key" {
		t.Errorf("create x-key = %q, want test-key", gotCreateKey)
	}
	if gotPollKey != "test-key" {
		t.Errorf("poll x-key = %q, want test-key", gotPollKey)
	}
	if atomic.LoadInt32(&pollCount) != 2 {
		t.Errorf("pollCount = %d, want 2", atomic.LoadInt32(&pollCount))
	}
	if len(resp.Images) != 1 {
		t.Fatalf("len(Images) = %d, want 1", len(resp.Images))
	}
	if string(resp.Images[0].Data) != string(sampleBytes) {
		t.Errorf("Data mismatch")
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", resp.Images[0].MediaType)
	}
}

func TestGenerateImages_WidthHeightFromSize(t *testing.T) {
	var gotBody map[string]any
	sampleBytes := []byte("bytes")

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Ready","result":{"sample":"` + srv.URL + `/sample"}}`))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat", Size: "1024x768"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotBody["width"] != float64(1024) {
		t.Errorf("width = %v, want 1024", gotBody["width"])
	}
	if gotBody["height"] != float64(768) {
		t.Errorf("height = %v, want 768", gotBody["height"])
	}
}

func TestGenerateImages_ErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Error"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for Error status")
	}
	if !strings.Contains(err.Error(), "Error") {
		t.Errorf("error = %q, want it to mention the status", err.Error())
	}
}

func TestGenerateImages_ContentModeratedStatus(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Content Moderated"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for Content Moderated status")
	}
	if !strings.Contains(err.Error(), "Content Moderated") {
		t.Errorf("error = %q, want it to mention the status", err.Error())
	}
}

func TestGenerateImages_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerateImages_PollNon2xxError(t *testing.T) {
	var pollHit int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollHit, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for non-2xx poll response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if got := atomic.LoadInt32(&pollHit); got != 1 {
		t.Errorf("poll hit count = %d, want 1 (loop should exit on first error)", got)
	}
}

func TestGenerateImages_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Pending"}`))
		select {
		case pollHit <- struct{}{}:
		default:
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	// A poll interval long enough that the test can cancel the context
	// while GenerateImages is sleeping between polls.
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(200*time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-pollHit
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := m.GenerateImages(ctx, provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestGenerateImages_ProviderOptionsMergeTopLevel(t *testing.T) {
	var gotBody map[string]any
	sampleBytes := []byte("bytes")

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Ready","result":{"sample":"` + srv.URL + `/sample"}}`))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"bfl": map[string]any{"safety_tolerance": 6},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotBody["safety_tolerance"] != float64(6) {
		t.Errorf("safety_tolerance = %v, want 6", gotBody["safety_tolerance"])
	}
}

// rewriteHostRoundTripper is a custom (non-*http.Transport) RoundTripper
// used by tests that need pollingURL to carry a hostname distinct from any
// real, dialable server (e.g. "api.us1.bfl.ai" or "attacker.evil.tld"):
// httptest servers only ever bind to loopback, so two of them can't be
// used to represent two genuinely different hostnames the way two real
// provider hosts would differ. RoundTrip records whether/with-what-key it
// was invoked, then (if a target is configured) rewrites the request onto
// the real loopback test server before actually sending it.
type rewriteHostRoundTripper struct {
	target *url.URL
	hits   int32
	gotKey string
}

func (rt *rewriteHostRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&rt.hits, 1)
	rt.gotKey = req.Header.Get("x-key")
	if rt.target == nil {
		return nil, errors.New("rewriteHostRoundTripper: no target configured for this request")
	}
	rewritten := req.Clone(req.Context())
	rewritten.URL.Scheme = rt.target.Scheme
	rewritten.URL.Host = rt.target.Host
	rewritten.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(rewritten)
}

// TestGenerateImages_ForeignRegistrableDomainPollingURLRejected covers the
// credential-leak vulnerability this task fixes: BFL's create call returns
// an absolute polling_url that the model then GETs with the x-key API key
// attached. If a malicious or MITM'd response pointed polling_url at an
// unrelated domain, the old code would happily send the API key there. The
// fix requires the polling_url to share the configured base URL's
// registrable domain before ever attaching x-key.
func TestGenerateImages_ForeignRegistrableDomainPollingURLRejected(t *testing.T) {
	rt := &rewriteHostRoundTripper{} // no target: must never be dialed

	p := New(WithAPIKey("secret-key"), WithBaseURL("https://api.bfl.ai"), WithHTTPClient(&http.Client{Transport: rt}))
	m := p.ImageModel("flux-pro-1.1").(*imageModel)

	_, _, err := m.poll(context.Background(), "https://attacker.evil.tld/poll")
	if err == nil {
		t.Fatal("expected error for foreign-registrable-domain polling_url")
	}
	if !strings.Contains(err.Error(), "registrable domain") {
		t.Errorf("error = %q, want it to mention registrable domain", err.Error())
	}
	if atomic.LoadInt32(&rt.hits) != 0 {
		t.Errorf("foreign host was hit %d times, want 0 (must reject before requesting)", rt.hits)
	}
	if rt.gotKey != "" {
		t.Errorf("x-key %q reached the foreign host, want it never attached", rt.gotKey)
	}
}

// TestGenerateImages_RegionalPollingURLSameRegistrableDomainWorks is the
// availability-side fix: BFL returns region-specific polling hosts (e.g.
// api.us1.bfl.ai, api.eu1.bfl.ai) alongside the api.bfl.ai base URL. An
// exact-host same-origin check would reject every real generation; the
// registrable-domain check must allow it (and still send the API key).
func TestGenerateImages_RegionalPollingURLSameRegistrableDomainWorks(t *testing.T) {
	regionSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Ready","result":{"sample":"https://api.us1.bfl.ai/sample"}}`))
	}))
	defer regionSrv.Close()

	target, err := url.Parse(regionSrv.URL)
	if err != nil {
		t.Fatalf("parse regionSrv.URL: %v", err)
	}
	rt := &rewriteHostRoundTripper{target: target}

	// Keep the test hermetic: the SSRF validator would otherwise do a live
	// DNS lookup of api.us1.bfl.ai. Point it at a public TEST-NET IP so the
	// registrable-domain gate + x-key attachment are exercised without any
	// real resolution (and so it passes in offline/air-gapped CI).
	restore := fetchmedia.SetLookupIPAddrForTest(func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	})
	defer restore()

	p := New(WithAPIKey("secret-key"), WithBaseURL("https://api.bfl.ai"), WithHTTPClient(&http.Client{Transport: rt}))
	m := p.ImageModel("flux-pro-1.1").(*imageModel)

	poll, _, err := m.poll(context.Background(), "https://api.us1.bfl.ai/poll")
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if poll.Status != "Ready" {
		t.Errorf("Status = %q, want Ready", poll.Status)
	}
	if rt.gotKey != "secret-key" {
		t.Errorf("x-key = %q, want secret-key", rt.gotKey)
	}
}

// TestGenerateImages_SameOriginPollingURLWorks is the positive counterpart to
// the foreign-domain rejection test: a polling_url on the exact same origin
// as the configured base URL (BFL's simplest legitimate response shape)
// must continue to poll successfully with the API key attached.
func TestGenerateImages_SameOriginPollingURLWorks(t *testing.T) {
	sampleBytes := []byte("sample-bytes")
	var gotPollKey string

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		gotPollKey = r.Header.Get("x-key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Ready","result":{"sample":"` + srv.URL + `/sample"}}`))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("secret-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotPollKey != "secret-key" {
		t.Errorf("poll x-key = %q, want secret-key", gotPollKey)
	}
	if len(resp.Images) != 1 || string(resp.Images[0].Data) != string(sampleBytes) {
		t.Fatalf("unexpected images: %+v", resp.Images)
	}
}

// TestGenerateImages_PollingURLLinkLocalRejected covers the SSRF half of the
// vulnerability: even a same-origin-looking polling_url that resolves to a
// link-local/metadata address must be rejected.
func TestGenerateImages_PollingURLLinkLocalRejected(t *testing.T) {
	// baseURL and polling_url share the same (link-local) host, so the
	// same-origin check alone wouldn't catch this -- it's the link-local
	// check that must reject it.
	p := New(WithAPIKey("k"), WithBaseURL("http://169.254.169.254"))
	m := p.ImageModel("flux-pro-1.1").(*imageModel)

	_, _, err := m.poll(context.Background(), "http://169.254.169.254/poll")
	if err == nil {
		t.Fatal("expected error for link-local polling_url")
	}
	if !strings.Contains(err.Error(), "link-local") && !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %q, want it to mention the link-local rejection", err.Error())
	}
}

func TestGenerateImages_EmptyPollingURLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":""}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for empty polling_url")
	}
	if !strings.Contains(err.Error(), "polling_url") {
		t.Errorf("error = %q, want it to mention the missing polling_url", err.Error())
	}
}
