package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Without the host allow-list in the create request, the VM gets unrestricted network.
func TestEgressFilterReachesCreateRequest(t *testing.T) {
	hosts := []string{"api.openai.com", "api.anthropic.com"}
	sent := []any{"api.openai.com", "api.anthropic.com"}
	cases := []struct {
		name   string
		create func(context.Context, *Smolvm) error
		hosts  []string
		want   any // nil: the field is absent
	}{
		{"exec with filter", runExec, hosts, sent},
		{"machine with filter", runMachine, hosts, sent},
		{"exec without filter", runExec, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var create map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/machines" {
					if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
						t.Errorf("decode create request: %v", err)
					}
				}
				w.Write([]byte(`{"name":"vm","exitCode":0}`))
			}))
			defer srv.Close()

			s := New(srv.URL)
			if tc.hosts != nil {
				s = s.WithEgressFilter(tc.hosts)
			}
			if err := tc.create(context.Background(), s); err != nil {
				t.Fatal(err)
			}

			if got := create["allowedHosts"]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("allowedHosts = %v, want %v", got, tc.want)
			}
		})
	}
}

func runExec(ctx context.Context, s *Smolvm) error {
	_, err := s.Exec(ctx, "alpine", []string{"true"}, nil, "", 0)
	return err
}

func runMachine(ctx context.Context, s *Smolvm) error {
	_, err := s.Machine(ctx, "vm", "")
	return err
}
