package websvc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/AdguardTeam/golibs/log"
	"github.com/AdguardTeam/golibs/timeutil"
)

// HTTP Settings Handlers

// TODO(a.garipov): !! Write tests!

// ReqPatchSettingsHTTP describes the request to the PATCH /api/v1/settings/http
// HTTP API.
type ReqPatchSettingsHTTP struct {
	// TODO(a.garipov): Add more as we go.
	//
	// TODO(a.garipov): Add wait time.

	Addresses       []netip.AddrPort  `json:"addresses"`
	SecureAddresses []netip.AddrPort  `json:"secure_addresses"`
	Timeout         timeutil.Duration `json:"timeout"`
}

// HTTPAPIHTTPSettings are the HTTP settings as used by the HTTP API.  See the
// HttpSettings object in the OpenAPI specification.
type HTTPAPIHTTPSettings struct {
	// TODO(a.garipov): Add more as we go.

	Addresses       []netip.AddrPort  `json:"addresses"`
	SecureAddresses []netip.AddrPort  `json:"secure_addresses"`
	Timeout         timeutil.Duration `json:"timeout"`
}

// handlePatchSettingsHTTP is the handler for the PATCH /api/v1/settings/http
// HTTP API.
func (svc *Service) handlePatchSettingsHTTP(w http.ResponseWriter, r *http.Request) {
	req := &ReqPatchSettingsHTTP{}

	// TODO(a.garipov): Validate nulls and proper JSON patch.

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		writeHTTPError(w, r, fmt.Errorf("decoding: %w", err))

		return
	}

	newConf := &Config{
		ConfigManager:   svc.confMgr,
		TLS:             svc.tls,
		Addresses:       req.Addresses,
		SecureAddresses: req.SecureAddresses,
		Timeout:         req.Timeout.Duration,
		ForceHTTPS:      svc.forceHTTPS,
	}

	writeJSONResponse(w, r, &HTTPAPIHTTPSettings{
		Addresses:       newConf.Addresses,
		SecureAddresses: newConf.SecureAddresses,
		Timeout:         timeutil.Duration{Duration: newConf.Timeout},
	})

	cancelUpd := func() {}
	updCtx := context.Background()

	ctx := r.Context()
	if deadline, ok := ctx.Deadline(); ok {
		updCtx, cancelUpd = context.WithDeadline(updCtx, deadline)
	}

	// Launch the new HTTP service in a separate goroutine to let this handler
	// finish and thus, this server to shutdown.
	go func() {
		defer cancelUpd()

		updErr := svc.confMgr.UpdateWeb(updCtx, newConf)
		if updErr != nil {
			writeHTTPError(w, r, fmt.Errorf("updating: %w", updErr))

			return
		}

		// TODO(a.garipov): !! Add some kind of timeout?  Context?
		var newSvc *Service
		for newSvc = svc.confMgr.Web(); newSvc == svc; {
			log.Debug("websvc: waiting for new websvc to be configured")
			time.Sleep(1 * time.Second)
		}

		updErr = newSvc.Start()
		if updErr != nil {
			log.Error("websvc: new svc failed to start: %s", updErr)
		}
	}()
}
