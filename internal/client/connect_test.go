package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/socket"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/sealjay/mcp-whatsapp/internal/security"
)

// newUnpairedClient returns a Client backed by a real (but unpaired)
// whatsmeow.Client. The device store has no ID, so IsLoggedIn returns
// false.
func newUnpairedClient() *Client {
	device := &wmstore.Device{}
	wa := whatsmeow.NewClient(device, NewStderrLogger("test", "ERROR", false))
	return &Client{
		wa:  wa,
		log: NewStderrLogger("test", "ERROR", false),
	}
}

func TestConnect_UnpairedRejectedByDefault(t *testing.T) {
	c := newUnpairedClient()
	err := c.Connect(context.Background(), ConnectOpts{})
	if err == nil {
		t.Fatal("expected error for unpaired device, got nil")
	}
	if !errors.Is(err, errNotPaired) {
		t.Fatalf("expected errNotPaired, got: %v", err)
	}
}

func TestConnect_AllowUnpairedSkipsGuard(t *testing.T) {
	// AllowUnpaired bypasses the "not paired" guard, so Connect proceeds
	// to whatsmeow's ConnectContext. We can't stub the underlying
	// websocket dial without a full mock of *whatsmeow.Client, so we
	// verify only that errNotPaired is NOT returned. ConnectContext will
	// fail with a network/context error — that's expected.
	c := newUnpairedClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so ConnectContext returns fast

	err := c.Connect(ctx, ConnectOpts{AllowUnpaired: true})
	if errors.Is(err, errNotPaired) {
		t.Fatal("AllowUnpaired should skip the paired-device guard")
	}
	// Any other error (context cancelled, dial failure) is fine.
}

func TestNewPairingSessionSharesDeviceStoreButIsolatesClient(t *testing.T) {
	c := newUnpairedClient()
	c.labelsEnabled = true

	session := c.NewPairingSession()
	if session == c {
		t.Fatal("pairing session reused the primary client wrapper")
	}
	if session.wa == c.wa {
		t.Fatal("pairing session reused the primary whatsmeow client")
	}
	if session.wa.Store != c.wa.Store {
		t.Fatal("pairing session did not share the paired device store")
	}
	if !session.labelsEnabled || !session.wa.EmitAppStateEventsOnFullSync {
		t.Fatal("pairing session did not preserve the labels configuration")
	}
}

// newOfflineClient returns a Client whose every WhatsApp dial is refused, as
// on a machine with no network, and a count of those dials. The test never
// reaches the network.
func newOfflineClient(t *testing.T, paired bool) (*Client, *atomic.Int32) {
	t.Helper()
	container, err := sqlstore.New(context.Background(), "sqlite3",
		"file:"+filepath.Join(t.TempDir(), "whatsapp.db")+"?_foreign_keys=on", waLog.Noop)
	if err != nil {
		t.Fatalf("sqlstore.New: %v", err)
	}
	t.Cleanup(func() { _ = container.Close() })
	device := container.NewDevice()
	if paired {
		device.ID = &types.JID{User: "447700000010", Device: 3, Server: types.DefaultUserServer}
	}

	logger := NewStderrLogger("test", "ERROR", false)
	c := &Client{log: logger, redactor: &security.Redactor{}}
	c.wa = newWAClient(device, logger, c.redactor, false)
	dials := new(atomic.Int32)
	offline := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}
		},
	}}
	c.wa.SetWebsocketHTTPClient(offline)
	c.wa.SetPreLoginHTTPClient(offline)
	return c, dials
}

// TestConnect_PairedKeepsRedialingWhileWhatsAppUnreachable covers a paired
// daemon that starts while the network is down: Connect must not fail, and
// the client must keep dialling WhatsApp in the background.
func TestConnect_PairedKeepsRedialingWhileWhatsAppUnreachable(t *testing.T) {
	c, dials := newOfflineClient(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Connect(ctx, ConnectOpts{}); err != nil {
		t.Fatalf("Connect while WhatsApp is unreachable: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for dials.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("client stopped dialling after %d attempt(s)", dials.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLogin_ReportsUnreachableWhatsApp covers `whatsapp-mcp login` without a
// network: it must fail with the dial error, not report success or a QR
// timeout.
func TestLogin_ReportsUnreachableWhatsApp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		paired bool
	}{
		{"paired", true},
		{"unpaired", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newOfflineClient(t, tc.paired)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Login(ctx, io.Discard); !errors.Is(err, socket.ErrDialFailed) {
				t.Fatalf("Login while WhatsApp is unreachable: want a dial failure, got %v", err)
			}
		})
	}
}
