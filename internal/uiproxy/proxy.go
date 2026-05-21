// Package uiproxy is the UI-side stand-in for the in-process app.App. In the
// collector/UI split (ADR 0001) the UI process binds *uiproxy.App into Wails
// instead of the real app.App; each method (generated in proxy_gen.go from
// app.App's signatures) forwards to the collector over the generic Invoke RPC.
// Because the signatures are identical, the Wails bindings and the frontend
// api layer are unchanged.
package uiproxy

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

//go:generate go run github.com/dusthoff/hashpoint/cmd/genuiproxy

// defaultCallTimeout bounds a single forwarded domain call. Long-running RPCs
// (Personio sync, plugin install) run server-side in the collector; this is the
// UI's patience for the round-trip, generous enough not to clip them.
const defaultCallTimeout = 5 * time.Minute

// App forwards frontend RPCs to the collector. Constructed by New over an
// established collector client; the generated methods call (*App).call.
type App struct {
	client  collectorpb.CollectorServiceClient
	timeout time.Duration
}

// New builds the proxy over a connected collector client.
func New(client collectorpb.CollectorServiceClient) *App {
	return &App{client: client, timeout: defaultCallTimeout}
}

// call marshals args to a JSON array, invokes method on the collector and
// decodes the JSON result into out (when non-nil). A domain error from the
// collector is returned as a plain error so the frontend sees exactly what the
// in-process method would have returned; a transport failure surfaces the same
// way.
func (a *App) call(method string, args []any, out any) error {
	var argsJSON []byte
	if len(args) > 0 {
		b, err := json.Marshal(args)
		if err != nil {
			return err
		}
		argsJSON = b
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()
	resp, err := a.client.Invoke(ctx, &collectorpb.InvokeRequest{Method: method, Args: argsJSON})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return err
		}
	}
	return nil
}

// callMulti is call for methods that return two or more non-error values: the
// collector marshals them as a JSON array, which is decoded element-by-element
// into outs (a slice of pointers to the return variables).
func (a *App) callMulti(method string, args []any, outs []any) error {
	var argsJSON []byte
	if len(args) > 0 {
		b, err := json.Marshal(args)
		if err != nil {
			return err
		}
		argsJSON = b
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()
	resp, err := a.client.Invoke(ctx, &collectorpb.InvokeRequest{Method: method, Args: argsJSON})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if len(resp.Result) == 0 {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		return err
	}
	for i, out := range outs {
		if i >= len(raw) {
			break
		}
		if err := json.Unmarshal(raw[i], out); err != nil {
			return err
		}
	}
	return nil
}
