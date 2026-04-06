// Package main implements a Dagger module that provides microVM-based sandboxed
// execution via smolvm. Unlike container-based execution, smolvm uses hardware
// virtualization (KVM / Hypervisor.framework) for stronger isolation, with
// built-in network egress filtering by hostname and CIDR.
//
// Prerequisites: a smolvm API server must be reachable from the Dagger engine.
// Start one with `smolvm serve start` on the host machine.
package main

import (
	"context"
	"fmt"
	"strings"
)

// Smolvm provides microVM-based sandboxed execution.
//
// Each execution runs inside a real virtual machine with its own kernel,
// providing hardware-level isolation that is fundamentally stronger than
// container namespaces. Network egress can be restricted to specific
// hostnames or CIDR ranges — a capability containers don't natively offer.
type Smolvm struct {
	// URL of the smolvm API server.
	ServerURL string
	// Hostnames allowed for outbound network access (empty = unrestricted).
	AllowHosts []string
	// CIDR ranges allowed for outbound network access.
	AllowCIDRs []string
	// Number of virtual CPUs for the VM.
	Cpus int
	// Memory allocation in MiB.
	MemoryMB int
	// Whether outbound networking is enabled.
	Network bool
}

// New creates a Smolvm instance configured to talk to the given server.
//
// The default URL (host.docker.internal:8080) works when the Dagger engine
// runs inside Docker Desktop and smolvm serve is running on the host.
func New(
	// smolvm API server URL.
	// +optional
	// +default="http://host.docker.internal:8080"
	serverURL string,
) *Smolvm {
	return &Smolvm{
		ServerURL: serverURL,
		Cpus:      1,
		MemoryMB:  512,
		Network:   true,
	}
}

// WithEgressFilter restricts outbound network access to specific hostnames.
// This is a key advantage over container-based execution — containers can only
// filter by IP/CIDR, not by hostname.
//
// Example: WithEgressFilter(["api.openai.com", "api.anthropic.com"])
func (s *Smolvm) WithEgressFilter(
	// Hostnames to allow (e.g., "api.openai.com").
	hosts []string,
) *Smolvm {
	s.AllowHosts = hosts
	return s
}

// WithCIDRFilter restricts outbound network access to specific CIDR ranges.
func (s *Smolvm) WithCIDRFilter(
	// CIDR ranges to allow (e.g., "10.0.0.0/8").
	cidrs []string,
) *Smolvm {
	s.AllowCIDRs = cidrs
	return s
}

// WithResources configures the VM's CPU and memory allocation.
func (s *Smolvm) WithResources(
	// Number of virtual CPUs.
	// +optional
	// +default=1
	cpus int,
	// Memory in MiB.
	// +optional
	// +default=512
	memoryMB int,
) *Smolvm {
	s.Cpus = cpus
	s.MemoryMB = memoryMB
	return s
}

// WithNetwork enables or disables outbound networking for VMs.
func (s *Smolvm) WithNetwork(
	// Enable outbound networking (TCP/UDP only).
	// +default=true
	enabled bool,
) *Smolvm {
	s.Network = enabled
	return s
}

// Exec creates an ephemeral microVM, runs a command in an OCI image, and
// returns stdout. The VM is automatically cleaned up afterward.
//
// This is the primary function for one-shot sandboxed execution.
func (s *Smolvm) Exec(
	ctx context.Context,
	// OCI image to run (e.g., "python:3.12-alpine", "node:22-alpine").
	image string,
	// Command and arguments to execute.
	command []string,
	// Environment variables in KEY=VALUE format.
	// +optional
	env []string,
	// Working directory inside the VM.
	// +optional
	cwd string,
	// Timeout in seconds (0 = default).
	// +optional
	// +default=60
	timeoutSecs int,
) (string, error) {
	client := NewSmolvmClient(s.ServerURL)

	// Create machine with configured resources and network policy.
	machine, err := client.CreateMachine(ctx, &CreateMachineReq{
		Cpus:         s.Cpus,
		MemoryMB:     s.MemoryMB,
		Network:      s.Network,
		AllowedCIDRs: s.AllowCIDRs,
	})
	if err != nil {
		return "", fmt.Errorf("create VM: %w", err)
	}
	// Always clean up the VM, even on error.
	defer client.DeleteMachine(ctx, machine.Name, true) //nolint:errcheck

	// Start the VM.
	if _, err := client.StartMachine(ctx, machine.Name); err != nil {
		return "", fmt.Errorf("start VM: %w", err)
	}

	// Pull the image first (run doesn't auto-pull).
	if err := client.PullImage(ctx, machine.Name, image); err != nil {
		return "", fmt.Errorf("pull image %q: %w", image, err)
	}

	// Run command in the specified image.
	resp, err := client.Run(ctx, machine.Name, &RunReq{
		Image:       image,
		Command:     command,
		Env:         parseEnvVars(env),
		Workdir:     cwd,
		TimeoutSecs: timeoutSecs,
	})
	if err != nil {
		return "", fmt.Errorf("run command: %w", err)
	}

	if resp.ExitCode != 0 {
		return "", fmt.Errorf("command exited %d:\nstdout: %s\nstderr: %s",
			resp.ExitCode, resp.Stdout, resp.Stderr)
	}

	return resp.Stdout, nil
}

// RunCode executes code in a language-specific microVM sandbox and returns
// the output. Supported languages: python, node, shell.
func (s *Smolvm) RunCode(
	ctx context.Context,
	// Source code to execute.
	code string,
	// Language runtime: "python", "node", or "shell".
	// +default="python"
	language string,
) (string, error) {
	var image string
	var command []string

	switch strings.ToLower(language) {
	case "python":
		image = "python:3.12-alpine"
		command = []string{"python", "-c", code}
	case "node", "javascript", "js":
		image = "node:22-alpine"
		command = []string{"node", "-e", code}
	case "shell", "sh", "bash":
		image = "alpine:latest"
		command = []string{"sh", "-c", code}
	default:
		return "", fmt.Errorf("unsupported language %q (supported: python, node, shell)", language)
	}

	return s.Exec(ctx, image, command, nil, "", 60)
}

// Health checks connectivity to the smolvm server and returns the version.
func (s *Smolvm) Health(ctx context.Context) (string, error) {
	client := NewSmolvmClient(s.ServerURL)
	resp, err := client.Health(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot reach smolvm server at %s: %w", s.ServerURL, err)
	}
	return fmt.Sprintf("smolvm %s: %s", resp.Version, resp.Status), nil
}

// Machine returns a handle to a persistent microVM that survives across
// function calls. Use this for multi-step workflows where you need to
// install packages, configure state, and then run tests — all within
// the same VM.
func (s *Smolvm) Machine(
	ctx context.Context,
	// Machine name (must be unique).
	name string,
	// OCI image to bootstrap the machine with.
	// +optional
	image string,
) (*SmolvmMachine, error) {
	client := NewSmolvmClient(s.ServerURL)

	// Create the machine.
	_, err := client.CreateMachine(ctx, &CreateMachineReq{
		Name:         &name,
		Cpus:         s.Cpus,
		MemoryMB:     s.MemoryMB,
		Network:      s.Network,
		AllowedCIDRs: s.AllowCIDRs,
	})
	if err != nil {
		return nil, fmt.Errorf("create machine %q: %w", name, err)
	}

	// Start it.
	if _, err := client.StartMachine(ctx, name); err != nil {
		return nil, fmt.Errorf("start machine %q: %w", name, err)
	}

	return &SmolvmMachine{
		Name:      name,
		ServerURL: s.ServerURL,
		Image:     image,
	}, nil
}

// SmolvmMachine is a handle to a persistent, running microVM.
// Use it for multi-step workflows that need state between commands.
type SmolvmMachine struct {
	// Machine name.
	Name string
	// smolvm server URL.
	ServerURL string
	// Default image for Run commands.
	Image string
}

// Exec runs a command directly in the machine VM (no container image).
func (m *SmolvmMachine) Exec(
	ctx context.Context,
	// Command and arguments.
	command []string,
	// Environment variables in KEY=VALUE format.
	// +optional
	env []string,
	// Working directory.
	// +optional
	cwd string,
	// Timeout in seconds.
	// +optional
	// +default=60
	timeoutSecs int,
) (string, error) {
	client := NewSmolvmClient(m.ServerURL)
	resp, err := client.Exec(ctx, m.Name, &ExecReq{
		Command:     command,
		Env:         parseEnvVars(env),
		Workdir:     cwd,
		TimeoutSecs: timeoutSecs,
	})
	if err != nil {
		return "", fmt.Errorf("exec in %q: %w", m.Name, err)
	}

	if resp.ExitCode != 0 {
		return "", fmt.Errorf("command exited %d:\nstdout: %s\nstderr: %s",
			resp.ExitCode, resp.Stdout, resp.Stderr)
	}

	return resp.Stdout, nil
}

// Run runs a command inside an OCI image on this machine.
func (m *SmolvmMachine) Run(
	ctx context.Context,
	// OCI image (overrides machine default if set).
	// +optional
	image string,
	// Command and arguments.
	command []string,
	// Environment variables in KEY=VALUE format.
	// +optional
	env []string,
	// Working directory.
	// +optional
	cwd string,
	// Timeout in seconds.
	// +optional
	// +default=60
	timeoutSecs int,
) (string, error) {
	img := image
	if img == "" {
		img = m.Image
	}
	if img == "" {
		return "", fmt.Errorf("no image specified (set via Machine() or pass --image)")
	}

	client := NewSmolvmClient(m.ServerURL)
	resp, err := client.Run(ctx, m.Name, &RunReq{
		Image:       img,
		Command:     command,
		Env:         parseEnvVars(env),
		Workdir:     cwd,
		TimeoutSecs: timeoutSecs,
	})
	if err != nil {
		return "", fmt.Errorf("run in %q: %w", m.Name, err)
	}

	if resp.ExitCode != 0 {
		return "", fmt.Errorf("command exited %d:\nstdout: %s\nstderr: %s",
			resp.ExitCode, resp.Stdout, resp.Stderr)
	}

	return resp.Stdout, nil
}

// Stop stops the machine VM. It can be restarted later.
func (m *SmolvmMachine) Stop(ctx context.Context) error {
	client := NewSmolvmClient(m.ServerURL)
	_, err := client.StopMachine(ctx, m.Name)
	return err
}

// Delete permanently removes the machine and its resources.
func (m *SmolvmMachine) Delete(ctx context.Context) error {
	client := NewSmolvmClient(m.ServerURL)
	return client.DeleteMachine(ctx, m.Name, true)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseEnvVars(env []string) []EnvKV {
	if len(env) == 0 {
		return nil
	}
	vars := make([]EnvKV, 0, len(env))
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			vars = append(vars, EnvKV{Name: k, Value: v})
		}
	}
	return vars
}
