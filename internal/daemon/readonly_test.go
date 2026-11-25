package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

var csrfField = regexp.MustCompile(`name="csrf_token" value="([0-9a-f]+)"`)

var noRedirectClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type httpResult struct {
	status int
	body   string
}

func startTestDaemon(t *testing.T, drv *fakePairDriver, readOnly bool) (*Server, string) {
	t.Helper()
	s, err := New(Config{Addr: "127.0.0.1:0", Driver: drv, ReadOnly: readOnly})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = s.Run(ctx) }()
	<-s.listenerOK
	return s, "http://" + s.BoundAddr()
}

func doRequest(t *testing.T, method, target string, form url.Values) httpResult {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: string(b)}
}

// A reset with a valid CSRF token unlinks the device in normal mode; a
// read-only daemon refuses it before the handler runs.
func TestServer_ReadOnlyRefusesPairReset(t *testing.T) {
	for _, tc := range []struct {
		readOnly    bool
		wantStatus  int
		wantLogouts int32
	}{
		{readOnly: false, wantStatus: http.StatusSeeOther, wantLogouts: 1},
		{readOnly: true, wantStatus: http.StatusForbidden, wantLogouts: 0},
	} {
		t.Run(fmt.Sprintf("read_only=%v", tc.readOnly), func(t *testing.T) {
			drv := newFakePairDriver(false)
			var logouts atomic.Int32
			drv.logoutFn = func(context.Context) error { logouts.Add(1); return nil }
			_, base := startTestDaemon(t, drv, tc.readOnly)

			page := doRequest(t, http.MethodGet, base+"/pair", nil)
			token := csrfField.FindStringSubmatch(page.body)
			if page.status != http.StatusOK || token == nil {
				t.Fatalf("GET /pair = %d without a CSRF token", page.status)
			}
			reset := doRequest(t, http.MethodPost, base+"/pair/reset", url.Values{"csrf_token": {token[1]}})
			if reset.status != tc.wantStatus || logouts.Load() != tc.wantLogouts {
				t.Fatalf("POST /pair/reset = %d with %d logouts, want %d with %d",
					reset.status, logouts.Load(), tc.wantStatus, tc.wantLogouts)
			}
		})
	}
}

// Read-only mode leaves the allowed routes as they are and answers 403 on
// every other path, including ones no handler serves.
func TestServer_ReadOnlyServesOnlyAllowedRoutes(t *testing.T) {
	routes := []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/healthz", true},
		{http.MethodGet, "/readyz", true},
		{http.MethodGet, "/pair", true},
		{http.MethodGet, "/pair/qr.png", true},
		{http.MethodPost, "/pair/code", true},
		{http.MethodPost, "/mcp", true},
		{http.MethodGet, "/pair/reset", false},
		{http.MethodGet, "/pair/", false},
		{http.MethodGet, "/pair/./reset", false},
		{http.MethodGet, "/mcp/x", false},
		{http.MethodGet, "/debug/pprof/", false},
	}
	statuses := map[bool][]int{}
	for _, readOnly := range []bool{false, true} {
		_, base := startTestDaemon(t, newFakePairDriver(false), readOnly)
		for _, r := range routes {
			statuses[readOnly] = append(statuses[readOnly], doRequest(t, r.method, base+r.path, nil).status)
		}
	}
	for i, r := range routes {
		normal, readOnly := statuses[false][i], statuses[true][i]
		if r.allowed && readOnly != normal {
			t.Errorf("%s %s: read-only = %d, normal = %d; an allowed route must not change", r.method, r.path, readOnly, normal)
		}
		if !r.allowed && (readOnly != http.StatusForbidden || normal == http.StatusForbidden) {
			t.Errorf("%s %s: read-only = %d, normal = %d; want 403 only in read-only mode", r.method, r.path, readOnly, normal)
		}
	}
}

func TestServer_ReadOnlyPairedPageHidesReset(t *testing.T) {
	s, base := startTestDaemon(t, newFakePairDriver(true), true)
	waitFor(t, s.Cache().Paired, "started paired")
	page := doRequest(t, http.MethodGet, base+"/pair", nil)
	if page.status != http.StatusOK || !strings.Contains(page.body, "Already paired") {
		t.Fatalf("GET /pair = %d, want the paired page: %s", page.status, page.body)
	}
	if strings.Contains(page.body, "/pair/reset") {
		t.Fatalf("read-only paired page offers a reset: %s", page.body)
	}
}
