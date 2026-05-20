// Package docker is the OCI runtime controller used by the daemon.
//
// Each Minecraft server is one container. Layout per server:
//
//	/var/lib/aethernet/templates/<template>/   (read-only base, bind-mounted)
//	/var/lib/aethernet/scratch/<server-id>/    (writable NVMe scratch, bind-mounted at /data)
//	tmpfs at /tmp inside the container (volatile logs)
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aethernet/aethernet/pkg/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	dockerunits "github.com/docker/go-units"
)

type Controller struct {
	cli    *client.Client
	log    *slog.Logger
	cfgDir Paths
}

type Paths struct {
	TemplateRoot string
	ScratchRoot  string
	Network      string
}

func New(logger *slog.Logger) (*Controller, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Controller{
		cli: cli,
		log: logger,
		cfgDir: Paths{
			TemplateRoot: "/var/lib/aethernet/templates",
			ScratchRoot:  "/var/lib/aethernet/scratch",
			Network:      "aethernet",
		},
	}, nil
}

func (c *Controller) SetPaths(p Paths) { c.cfgDir = p }

// StartServer creates and starts a container for the given spec.
// Returns the container ID and the host port the Minecraft port was mapped to.
func (c *Controller) StartServer(ctx context.Context, spec types.ServerSpec) (string, uint32, error) {
	if c == nil {
		return "", 0, ErrDisabled
	}
	scratch := filepath.Join(c.cfgDir.ScratchRoot, spec.ID)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", 0, fmt.Errorf("scratch mkdir: %w", err)
	}
	templateBase := filepath.Join(c.cfgDir.TemplateRoot, spec.TemplateID)

	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   templateBase,
			Target:   "/template",
			ReadOnly: true,
		},
		{
			Type:   mount.TypeBind,
			Source: scratch,
			Target: "/data",
		},
		{
			Type:   mount.TypeTmpfs,
			Target: "/tmp",
			TmpfsOptions: &mount.TmpfsOptions{
				SizeBytes: 64 * 1024 * 1024,
				Mode:      0o1777,
			},
		},
	}

	hostPort, err := pickFreePort()
	if err != nil {
		return "", 0, err
	}
	portKey := strconv.Itoa(int(spec.MinecraftPort)) + "/tcp"
	exposed := map[string]struct{}{portKey: {}}
	portMap := map[string][]container_PortBinding{
		portKey: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(int(hostPort))}},
	}

	env := []string{
		"EULA=TRUE",
		"AETHERNET_SERVER_ID=" + spec.ID,
		"AETHERNET_TEMPLATE=" + spec.TemplateID,
	}
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	cfg := &container.Config{
		Image:        spec.Image,
		Env:          env,
		ExposedPorts: convertExposed(exposed),
		Labels: map[string]string{
			"aethernet.server_id":   spec.ID,
			"aethernet.template_id": spec.TemplateID,
			"aethernet.group_id":    spec.GroupID,
		},
		WorkingDir:   "/data",
		AttachStdout: true,
		AttachStderr: true,
	}

	memBytes, _ := dockerunits.RAMInBytes(strconv.FormatUint(spec.MemoryMB, 10) + "m")
	if memBytes == 0 {
		memBytes = 2 * 1024 * 1024 * 1024
	}
	hostCfg := &container.HostConfig{
		Mounts:       mounts,
		PortBindings: convertPortMap(portMap),
		Resources: container.Resources{
			Memory:   memBytes,
			NanoCPUs: int64(spec.CPUQuota * 1e9),
		},
		AutoRemove:    false,
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		NetworkMode:   container.NetworkMode(c.cfgDir.Network),
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			c.cfgDir.Network: {},
		},
	}

	resp, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, containerName(spec.ID))
	if err != nil {
		return "", 0, fmt.Errorf("container create: %w", err)
	}
	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", 0, fmt.Errorf("container start: %w", err)
	}
	c.log.Info("container started", "server_id", spec.ID, "container_id", resp.ID, "host_port", hostPort)
	return resp.ID, hostPort, nil
}

// StopServer issues a graceful stop, then SIGKILL after grace seconds.
func (c *Controller) StopServer(ctx context.Context, serverID string, graceSec uint32) error {
	if c == nil {
		return ErrDisabled
	}
	g := int(graceSec)
	if g <= 0 {
		g = 30
	}
	timeout := g
	return c.cli.ContainerStop(ctx, containerName(serverID), container.StopOptions{Timeout: &timeout})
}

func (c *Controller) RemoveServer(ctx context.Context, serverID string, purgeVolume bool) error {
	if c == nil {
		return ErrDisabled
	}
	err := c.cli.ContainerRemove(ctx, containerName(serverID), container.RemoveOptions{Force: true})
	if err != nil && !client.IsErrNotFound(err) {
		return err
	}
	if purgeVolume {
		return os.RemoveAll(filepath.Join(c.cfgDir.ScratchRoot, serverID))
	}
	return nil
}

// ContainerDataPath returns the host path bind-mounted at /data inside the
// container. Used by the SFTP server to chroot users into their server.
func (c *Controller) ContainerDataPath(serverID string) string {
	return filepath.Join(c.cfgDir.ScratchRoot, serverID)
}

// StreamLogs writes container stdout+stderr (demuxed) into dst.
func (c *Controller) StreamLogs(ctx context.Context, serverID string, follow bool, tailLines uint32, dst io.Writer) error {
	if c == nil {
		return ErrDisabled
	}
	tail := "all"
	if tailLines > 0 {
		tail = strconv.FormatUint(uint64(tailLines), 10)
	}
	rc, err := c.cli.ContainerLogs(ctx, containerName(serverID), container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
	})
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = stdcopy.StdCopy(dst, dst, rc)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Exec runs a command inside a running container (e.g. "say Hello").
func (c *Controller) Exec(ctx context.Context, serverID, cmd string) (string, int, error) {
	if c == nil {
		return "", -1, ErrDisabled
	}
	exec, err := c.cli.ContainerExecCreate(ctx, containerName(serverID), container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/bin/sh", "-c", cmd},
	})
	if err != nil {
		return "", -1, err
	}
	hijack, err := c.cli.ContainerExecAttach(ctx, exec.ID, container.ExecStartOptions{})
	if err != nil {
		return "", -1, err
	}
	defer hijack.Close()
	var buf bytesBuf
	if _, err := stdcopy.StdCopy(&buf, &buf, hijack.Reader); err != nil && !errors.Is(err, io.EOF) {
		return "", -1, err
	}
	insp, _ := c.cli.ContainerExecInspect(ctx, exec.ID)
	return buf.String(), insp.ExitCode, nil
}

func containerName(id string) string { return "aether-" + id }

var ErrDisabled = errors.New("docker: controller not available")

// container_PortBinding mirrors nat.PortBinding without importing the docker/go-connections package
// at this layer.
type container_PortBinding struct {
	HostIP   string
	HostPort string
}
