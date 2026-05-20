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
	RedisPassword string   `yaml:"redis_password"`
	MariaDBDSN    string   `yaml:"mariadb_dsn"`  // full DSN, takes priority
	MariaDBAddr   string   `yaml:"mariadb_addr"` // legacy fallback
	MariaDBUser   string   `yaml:"mariadb_user"`
	MariaDBPass   string   `yaml:"mariadb_pass"`

	Docker DockerConfig `yaml:"docker"`
}

type DockerConfig struct {
	Endpoint     string `yaml:"endpoint"`      // unix:///var/run/docker.sock
	ScratchPath  string `yaml:"scratch_path"`  // /var/lib/aethernet/scratch
	TemplatePath string `yaml:"template_path"` // /var/lib/aethernet/templates
	NetworkName  string `yaml:"network_name"`
}

// Default returns a Config pre-populated from environment variables, with
// sensible fallbacks. Environment variables (set by docker-compose) always
// win over the YAML file so containers need no config file at all.
func Default() *Config {
	host, _ := os.Hostname()
	if host == "" {
		host = "node"
	}
	nodeID := env("AETHERNET_NODE_ID", sanitizeID(host))
	return &Config{
		NodeID:            nodeID,
		DataDir:           env("AETHERNET_DATA_DIR", "/var/lib/aethernet"),
		RaftBindAddr:      env("AETHERNET_RAFT_BIND", "0.0.0.0:7000"),
		RaftAdvertiseAddr: env("AETHERNET_RAFT_ADVERTISE", nodeID+":7000"),
		GRPCListen:        env("AETHERNET_GRPC_LISTEN", "0.0.0.0:7001"),
		HTTPListen:        env("AETHERNET_HTTP_LISTEN", "0.0.0.0:8081"),
		SFTPListen:        env("AETHERNET_SFTP_LISTEN", "0.0.0.0:2022"),
		RedisAddrs:        strings.Split(env("AETHERNET_REDIS_ADDRS", "127.0.0.1:6379"), ","),
		RedisPassword:     env("AETHERNET_REDIS_PASSWORD", ""),
		MariaDBDSN:        env("AETHERNET_MARIADB_DSN", ""),
		MariaDBAddr:       env("AETHERNET_MARIADB_ADDR", "127.0.0.1:3306"),
		MariaDBUser:       env("AETHERNET_MARIADB_USER", "aethernet"),
		MariaDBPass:       env("AETHERNET_MARIADB_PASS", ""),
		Docker: DockerConfig{
			Endpoint:     env("DOCKER_HOST", "unix:///var/run/docker.sock"),
			ScratchPath:  "/var/lib/aethernet/scratch",
			TemplatePath: "/var/lib/aethernet/templates",
			NetworkName:  "aethernet",
		},
	}
}

// DSN returns the database DSN, preferring the explicit DSN env var.
func (c *Config) DSN() string {
	if c.MariaDBDSN != "" {
		return c.MariaDBDSN
	}
	return fmt.Sprintf("%s:%s@tcp(%s)/aethernet?parseTime=true&multiStatements=false",
		c.MariaDBUser, c.MariaDBPass, c.MariaDBAddr)
}

// Load reads a YAML config file, then overlays environment variables.
// If path does not exist we use pure env/defaults (works in Docker).
func Load(path string) (*Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil // no file → pure env config, fine for containers
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Re-apply env overrides so container env always beats the YAML file.
	if v := os.Getenv("AETHERNET_NODE_ID"); v != "" {
		c.NodeID = v
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
