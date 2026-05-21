package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

var (
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
)

// Invoker dispatches a named method call on a target object, marshalling
// arguments and results as JSON. It backs the collector's generic Invoke RPC:
// the UI sends a method name plus a JSON array of arguments, the collector
// looks up the matching exported method on the real app.App and calls it by
// reflection. This mirrors how Wails already crosses the JS/Go boundary
// (reflection + JSON), so every bound method is reachable without a per-method
// gRPC contract (ADR 0001, Phase 2).
type Invoker struct {
	methods map[string]reflect.Value
}

// NewInvoker indexes the exported methods of target (in production *app.App)
// for dispatch by name.
func NewInvoker(target any) *Invoker {
	v := reflect.ValueOf(target)
	t := v.Type()
	methods := make(map[string]reflect.Value, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if m.IsExported() {
			methods[m.Name] = v.Method(i)
		}
	}
	return &Invoker{methods: methods}
}

// Invoke calls method by name. argsJSON is a JSON array whose elements decode
// into the method's parameters in order; a context.Context parameter is
// injected from ctx rather than decoded. The returned bytes are the JSON of
// the method's single non-error return value (nil when it returns nothing or
// only an error). A domain error returned by the method is propagated as-is so
// the caller can forward its message to the UI.
func (inv *Invoker) Invoke(ctx context.Context, method string, argsJSON []byte) ([]byte, error) {
	fn, ok := inv.methods[method]
	if !ok {
		return nil, fmt.Errorf("unknown method %q", method)
	}
	ft := fn.Type()

	var raw []json.RawMessage
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &raw); err != nil {
			return nil, fmt.Errorf("decode args for %s: %w", method, err)
		}
	}

	in := make([]reflect.Value, ft.NumIn())
	argIdx := 0
	for i := 0; i < ft.NumIn(); i++ {
		pt := ft.In(i)
		if pt == contextType {
			in[i] = reflect.ValueOf(ctx)
			continue
		}
		if argIdx >= len(raw) {
			return nil, fmt.Errorf("%s: not enough arguments (have %d)", method, len(raw))
		}
		p := reflect.New(pt)
		if err := json.Unmarshal(raw[argIdx], p.Interface()); err != nil {
			return nil, fmt.Errorf("decode arg %d for %s: %w", argIdx, method, err)
		}
		in[i] = p.Elem()
		argIdx++
	}
	if argIdx != len(raw) {
		return nil, fmt.Errorf("%s: got %d arguments, want %d", method, len(raw), argIdx)
	}

	out := fn.Call(in)

	var result any
	for _, rv := range out {
		if rv.Type() == errorType {
			if !rv.IsNil() {
				return nil, rv.Interface().(error)
			}
			continue
		}
		result = rv.Interface()
	}
	if result == nil {
		return nil, nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode result of %s: %w", method, err)
	}
	return data, nil
}
