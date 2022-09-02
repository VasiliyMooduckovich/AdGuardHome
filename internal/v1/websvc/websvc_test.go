package websvc_test

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/AdguardTeam/AdGuardHome/internal/aghtest"
	"github.com/AdguardTeam/AdGuardHome/internal/v1/dnssvc"
	"github.com/AdguardTeam/AdGuardHome/internal/v1/websvc"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	aghtest.DiscardLogOutput(m)
}

// testTimeout is the common timeout for tests.
const testTimeout = 1 * time.Second

// testStart is the server start value for tests.
var testStart = time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

// type check
var _ websvc.ConfigManager = (*configManager)(nil)

// configManager is a [websvc.ConfigManager] for tests.
type configManager struct {
	onDNS func() (svc *dnssvc.Service)
	onWeb func() (svc *websvc.Service)

	onUpdateDNS func(ctx context.Context, c *dnssvc.Config) (err error)
	onUpdateWeb func(ctx context.Context, c *websvc.Config) (err error)
}

// DNS implements the [websvc.ConfigManager] interface for *configManager.
func (m *configManager) DNS() (svc *dnssvc.Service) {
	return m.onDNS()
}

// Web implements the [websvc.ConfigManager] interface for *configManager.
func (m *configManager) Web() (svc *websvc.Service) {
	return m.onWeb()
}

// UpdateDNS implements the [websvc.ConfigManager] interface for *configManager.
func (m *configManager) UpdateDNS(ctx context.Context, c *dnssvc.Config) (err error) {
	return m.onUpdateDNS(ctx, c)
}

// UpdateWeb implements the [websvc.ConfigManager] interface for *configManager.
func (m *configManager) UpdateWeb(ctx context.Context, c *websvc.Config) (err error) {
	return m.onUpdateWeb(ctx, c)
}

// newConfigManager returns a *configManager all methods of which panic.
func newConfigManager() (m *configManager) {
	return &configManager{
		onDNS: func() (svc *dnssvc.Service) { panic("not implemented") },
		onWeb: func() (svc *websvc.Service) { panic("not implemented") },
		onUpdateDNS: func(_ context.Context, _ *dnssvc.Config) (err error) {
			panic("not implemented")
		},
		onUpdateWeb: func(_ context.Context, _ *websvc.Config) (err error) {
			panic("not implemented")
		},
	}
}

// newTestServer creates and starts a new web service instance as well as its
// sole address.  It also registers a cleanup procedure, which shuts the
// instance down.
//
// TODO(a.garipov): Use svc or remove it.
func newTestServer(
	t testing.TB,
	confMgr websvc.ConfigManager,
) (svc *websvc.Service, addr netip.AddrPort) {
	t.Helper()

	c := &websvc.Config{
		ConfigManager:   confMgr,
		TLS:             nil,
		Addresses:       []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:0")},
		SecureAddresses: nil,
		Timeout:         testTimeout,
		Start:           testStart,
		ForceHTTPS:      false,
	}

	svc = websvc.New(c)

	err := svc.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)

		err = svc.Shutdown(ctx)
		require.NoError(t, err)
	})

	c = svc.Config()
	require.NotNil(t, c)
	require.Len(t, c.Addresses, 1)

	return svc, c.Addresses[0]
}

// httpGet is a helper that performs an HTTP GET request and returns the body of
// the response as well as checks that the status code is correct.
//
// TODO(a.garipov): Add helpers for other methods.
func httpGet(t testing.TB, u *url.URL, wantCode int) (body []byte) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	require.NoErrorf(t, err, "creating req")

	httpCli := &http.Client{
		Timeout: testTimeout,
	}
	resp, err := httpCli.Do(req)
	require.NoErrorf(t, err, "performing req")
	require.Equal(t, wantCode, resp.StatusCode)

	testutil.CleanupAndRequireSuccess(t, resp.Body.Close)

	body, err = io.ReadAll(resp.Body)
	require.NoErrorf(t, err, "reading body")

	return body
}

func TestService_Start_getHealthCheck(t *testing.T) {
	confMgr := newConfigManager()
	_, addr := newTestServer(t, confMgr)
	u := &url.URL{
		Scheme: "http",
		Host:   addr.String(),
		Path:   websvc.PathHealthCheck,
	}

	body := httpGet(t, u, http.StatusOK)

	assert.Equal(t, []byte("OK"), body)
}
