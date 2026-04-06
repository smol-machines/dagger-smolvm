package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SmolvmClient is an HTTP client for the smolvm API server.
type SmolvmClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewSmolvmClient creates a client targeting the given smolvm server URL.
func NewSmolvmClient(baseURL string) *SmolvmClient {
	return &SmolvmClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// ---------------------------------------------------------------------------
// API request types (match smolvm src/api/types.rs, camelCase JSON)
// ---------------------------------------------------------------------------

// CreateMachineReq creates a new machine.
type CreateMachineReq struct {
	Name         *string  `json:"name,omitempty"`
	Cpus         int      `json:"cpus,omitempty"`
	MemoryMB     int      `json:"memoryMb,omitempty"`
	Network      bool     `json:"network"`
	StorageGB    *int     `json:"storageGb,omitempty"`
	OverlayGB    *int     `json:"overlayGb,omitempty"`
	AllowedCIDRs []string `json:"allowedCidrs,omitempty"`
}

// RunReq runs a command inside an OCI image on a machine.
type RunReq struct {
	Image       string   `json:"image"`
	Command     []string `json:"command"`
	Env         []EnvKV  `json:"env,omitempty"`
	Workdir     string   `json:"workdir,omitempty"`
	TimeoutSecs int      `json:"timeoutSecs,omitempty"`
}

// ExecReq executes a command directly in the machine VM.
type ExecReq struct {
	Command     []string `json:"command"`
	Env         []EnvKV  `json:"env,omitempty"`
	Workdir     string   `json:"workdir,omitempty"`
	TimeoutSecs int      `json:"timeoutSecs,omitempty"`
}

// EnvKV is a single environment variable.
type EnvKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ---------------------------------------------------------------------------
// API response types
// ---------------------------------------------------------------------------

// MachineInfoResp is the machine status returned by create/start/stop/get.
type MachineInfoResp struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Cpus      int    `json:"cpus"`
	MemoryMB  int    `json:"memoryMb"`
	Network   bool   `json:"network"`
	CreatedAt string `json:"createdAt"`
}

// ExecResp is the result of running a command.
type ExecResp struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// HealthResp is the server health check response.
type HealthResp struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// APIErr is the error body returned by the smolvm API on 4xx/5xx.
type APIErr struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// ---------------------------------------------------------------------------
// Client methods
// ---------------------------------------------------------------------------

// Health checks connectivity to the smolvm server.
func (c *SmolvmClient) Health(ctx context.Context) (*HealthResp, error) {
	var resp HealthResp
	if err := c.get(ctx, "/health", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateMachine creates a new machine and returns its info.
func (c *SmolvmClient) CreateMachine(ctx context.Context, req *CreateMachineReq) (*MachineInfoResp, error) {
	var info MachineInfoResp
	if err := c.post(ctx, "/api/v1/machines", req, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// StartMachine starts a stopped/created machine.
func (c *SmolvmClient) StartMachine(ctx context.Context, name string) (*MachineInfoResp, error) {
	var info MachineInfoResp
	if err := c.post(ctx, "/api/v1/machines/"+name+"/start", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// StopMachine stops a running machine.
func (c *SmolvmClient) StopMachine(ctx context.Context, name string) (*MachineInfoResp, error) {
	var info MachineInfoResp
	if err := c.post(ctx, "/api/v1/machines/"+name+"/stop", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// DeleteMachine deletes a machine. If force is true, deletes even if running.
func (c *SmolvmClient) DeleteMachine(ctx context.Context, name string, force bool) error {
	path := "/api/v1/machines/" + name
	if force {
		path += "?force=true"
	}
	return c.del(ctx, path)
}

// Run executes a command inside an OCI image on a machine.
func (c *SmolvmClient) Run(ctx context.Context, machine string, req *RunReq) (*ExecResp, error) {
	var resp ExecResp
	if err := c.post(ctx, "/api/v1/machines/"+machine+"/run", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PullImage pulls an OCI image into a machine's local store.
func (c *SmolvmClient) PullImage(ctx context.Context, machine string, image string) error {
	type pullReq struct {
		Image string `json:"image"`
	}
	return c.post(ctx, "/api/v1/machines/"+machine+"/images/pull", &pullReq{Image: image}, nil)
}

// Exec executes a command directly in the machine VM.
func (c *SmolvmClient) Exec(ctx context.Context, machine string, req *ExecReq) (*ExecResp, error) {
	var resp ExecResp
	if err := c.post(ctx, "/api/v1/machines/"+machine+"/exec", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (c *SmolvmClient) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, result)
}

func (c *SmolvmClient) post(ctx context.Context, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, result)
}

func (c *SmolvmClient) del(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *SmolvmClient) do(req *http.Request, result any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("smolvm request failed (is smolvm serve running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var apiErr APIErr
		if decErr := json.NewDecoder(resp.Body).Decode(&apiErr); decErr != nil {
			return fmt.Errorf("smolvm HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("smolvm error [%s]: %s", apiErr.Code, apiErr.Error)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
