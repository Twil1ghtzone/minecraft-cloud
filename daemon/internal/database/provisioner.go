// Package database — provisioner.go
// Dynamic database and user provisioner for MariaDB Galera.
package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

// Provisioner creates and drops isolated MariaDB databases and users.
type Provisioner struct {
	db *sql.DB
}

// NewProvisioner connects as the admin user and returns a Provisioner.
func NewProvisioner(dsn string) (*Provisioner, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("provisioner: open: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("provisioner: ping: %w", err)
	}
	return &Provisioner{db: db}, nil
}

// ProvisionResult contains the newly created database credentials.
type ProvisionResult struct {
	DatabaseName string
	Username     string
	Password     string
}

// Create provisions a new isolated MariaDB database and a dedicated user
// with full privileges scoped strictly to that database.
func (p *Provisioner) Create(ctx context.Context, dbName, username string) (*ProvisionResult, error) {
	if !isValidIdent(dbName) || !isValidIdent(username) {
		return nil, errors.New("provisioner: invalid database or username (alphanum + underscore only, max 64 chars)")
	}
	password, err := generatePassword(24)
	if err != nil {
		return nil, fmt.Errorf("provisioner: generate password: %w", err)
	}

	queries := []string{
		// Backtick-quote the identifier to prevent injection even though we validated
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dbName),
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'", username, password),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'", dbName, username),
		"FLUSH PRIVILEGES",
	}

	for _, q := range queries {
		if _, err := p.db.ExecContext(ctx, q); err != nil {
			return nil, fmt.Errorf("provisioner: %q: %w", q, err)
		}
	}
	return &ProvisionResult{
		DatabaseName: dbName,
		Username:     username,
		Password:     password,
	}, nil
}

// Drop removes the database and its dedicated user.
func (p *Provisioner) Drop(ctx context.Context, dbName, username string) error {
	if !isValidIdent(dbName) || !isValidIdent(username) {
		return errors.New("provisioner: invalid identifier")
	}
	queries := []string{
		fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName),
		fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", username),
		"FLUSH PRIVILEGES",
	}
	for _, q := range queries {
		if _, err := p.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("provisioner drop %q: %w", q, err)
		}
	}
	return nil
}

// SizeBytes returns the approximate on-disk size of a database in bytes.
func (p *Provisioner) SizeBytes(ctx context.Context, dbName string) (uint64, error) {
	var size sql.NullInt64
	err := p.db.QueryRowContext(ctx,
		`SELECT SUM(data_length + index_length)
		 FROM information_schema.TABLES
		 WHERE table_schema = ?`, dbName).Scan(&size)
	if err != nil {
		return 0, err
	}
	if size.Valid {
		return uint64(size.Int64), nil
	}
	return 0, nil
}

func generatePassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:n], nil
}

func isValidIdent(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}
