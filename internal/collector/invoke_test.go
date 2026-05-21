package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeAPI struct{}

func (fakeAPI) Echo(s string) string                        { return "echo:" + s }
func (fakeAPI) Add(a, b int) int                            { return a + b }
func (fakeAPI) Fail() error                                 { return errors.New("boom") }
func (fakeAPI) GetThing(id int64) (map[string]int64, error) { return map[string]int64{"id": id}, nil }
func (fakeAPI) Nothing()                                    {}
func (fakeAPI) WithCtx(ctx context.Context, s string) (string, error) {
	if ctx == nil {
		return "", errors.New("nil ctx")
	}
	return "ctx:" + s, nil
}

func TestInvoker(t *testing.T) {
	t.Parallel()
	inv := NewInvoker(fakeAPI{})
	ctx := context.Background()

	cases := []struct {
		name    string
		method  string
		args    string
		want    string // expected result JSON; "" means nil result
		wantErr string // substring; "" means no error
	}{
		{"string return", "Echo", `["hi"]`, `"echo:hi"`, ""},
		{"int args and return", "Add", `[2,3]`, `5`, ""},
		{"error return", "Fail", `[]`, "", "boom"},
		{"struct return", "GetThing", `[42]`, `{"id":42}`, ""},
		{"no return", "Nothing", `[]`, "", ""},
		{"context injected, not decoded", "WithCtx", `["x"]`, `"ctx:x"`, ""},
		{"unknown method", "Nope", `[]`, "", "unknown method"},
		{"too few args", "Add", `[1]`, "", "not enough arguments"},
		{"too many args", "Echo", `["a","b"]`, "", "want"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := inv.Invoke(ctx, tc.method, []byte(tc.args))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("result = %s, want %s", got, tc.want)
			}
		})
	}
}
