package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	NodeID            string `yaml:"node_id"`
	DataDir           string `yaml:"data_dir"`
	RaftBindAddr      string `yaml:"raft_bind_addr"`
	RaftAdvertiseAddr string `yaml:"raft_advertise_addr"`
	GRPCListen        string `yaml:"grpc_listen"`
	HTTPListen        string `yaml:"http_listen"`
	SFTPListen        string `yaml:"sftp_listen"`

	RedisAddrs    []string `yaml:"redis_addrs"`
	MariaDBAddr   string   `yaml:"mariadb_addr"`
	MariaDBUser   string   `yaml:"mariadb_user"`
	MariaDBPass   string   `yaml:"mariadb_pass"`

	Docker DockerConfig `yaml:"docker"`
}

type DockerConfig struct {
	Endpoint       string `yaml:"endpoint"`         // unix:///var/run/docker.sock
	ScratchPath    string `yaml:"scratch_path"`     // /var/lib/aethernet/scratch
	TemplatePath   string `yaml:"template_path"`    // /var/lib/aethernet/templates
	NetworkName    string `yaml:"network_name"`
}

func Default() *Config {
	host, _ := os.Hostname()
	if host == "" {
		host = "node"
	}
	return &Config{
		NodeID:            sanitizeID(host),
		DataDir:           "/var/lib/aethernet",
		RaftBindAddr:      "0.0.0.0:7000",
		RaftAdvertiseAddr: host + ":7000",
		GRPCListen:        "0.0.0.0:7001",
		HTTPListen:        "0.0.0.0:8080",
		SFTPListen:        "0.0.0.0:2022",
		Docker: DockerConfig{
			Endpoint:     "unix:///var/run/docker.sock",
			ScratchPath:  "/var/lib/aethernet/scratch",
			TemplatePath: "/var/lib/aethernet/templates",
			NetworkName:  "aethernet",
		},
	}
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := Default()
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.NodeID == "" {
		return nil, fmt.Errorf("config: node_id is required")
	}
	return c, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
