// Package database provisions tenant MySQL/MariaDB databases against the
// Galera cluster. The Galera cluster itself is brought up by the installer
// (Module 9); this package only adds, drops and queries.
//
// All connections go through the local Galera node; Galera handles the
// multi-master replication transparently.
package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/aethernet/aethernet/pkg/types"
	_ "github.com/go-sql-driver/mysql"
)

type Provisioner struct {
	db   *sql.DB
	log  *slog.Logger
	host string
	port uint32
}

type Config struct {
	DSN  string // admin DSN with create/grant rights
	Host string // user-facing host (e.g. cluster VIP)
	Port uint32
	Log  *slog.Logger
}

func New(cfg Config) (*Provisioner, error) {
	if cfg.DSN == "" {
		return nil, errors.New("empty DSN")
	}
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping galera: %w", err)
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Provisioner{db: db, log: cfg.Log, host: cfg.Host, port: cfg.Port}, nil
}

func (p *Provisioner) Close() error { return p.db.Close() }

var ident = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)

// Create provisions a new database + user and returns the generated password.
// The password is shown to the user once and is never persisted in plaintext.
func (p *Provisioner) Create(ctx context.Context, name string) (types.Database, string, error) {
	if !ident.MatchString(name) {
		return types.Database{}, "", fmt.Errorf("invalid database name %q (must match ^[a-zA-Z][a-zA-Z0-9_]{0,31}$)", name)
	}
	user := name + "_u"
	pwd, err := randomPassword(24)
	if err != nil {
		return types.Database{}, "", err
	}

	// Galera is a single logical write target; we use a single transaction-like
	// sequence of DDL statements. DDL is auto-committed, so we wrap in a
	// best-effort cleanup on partial failure.
	stmts := []string{
		"CREATE DATABASE IF NOT EXISTS `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		"CREATE USER IF NOT EXISTS '" + user + "'@'%' IDENTIFIED BY '" + escape(pwd) + "'",
		"GRANT ALL PRIVILEGES ON `" + name + "`.* TO '" + user + "'@'%'",
		"FLUSH PRIVILEGES",
	}
	for _, stmt := range stmts {
		if _, err := p.db.ExecContext(ctx, stmt); err != nil {
			p.log.Warn("ddl failed, attempting cleanup", "stmt", stmt, "err", err)
			_, _ = p.db.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+name+"`")
			_, _ = p.db.ExecContext(ctx, "DROP USER IF EXISTS '"+user+"'@'%'")
			return types.Database{}, "", err
		}
	}

	return types.Database{
		ID:        "db_" + name,
		Name:      name,
		Engine:    "mariadb",
		Username:  user,
		Host:      p.host,
		Port:      p.port,
		CreatedAt: time.Now(),
	}, pwd, nil
}

func (p *Provisioner) Drop(ctx context.Context, name string) error {
	if !ident.MatchString(name) {
		return fmt.Errorf("invalid name")
	}
	user := name + "_u"
	_, err := p.db.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+name+"`")
	if err != nil {
		return err
	}
	_, _ = p.db.ExecContext(ctx, "DROP USER IF EXISTS '"+user+"'@'%'")
	return nil
}

// SizeBytes reports the on-disk size of a single database.
func (p *Provisioner) SizeBytes(ctx context.Context, name string) (uint64, error) {
	if !ident.MatchString(name) {
		return 0, fmt.Errorf("invalid name")
	}
	row := p.db.QueryRowContext(ctx,
		"SELECT IFNULL(SUM(data_length + index_length), 0) FROM information_schema.tables WHERE table_schema = ?",
		name,
	)
	var n uint64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}
