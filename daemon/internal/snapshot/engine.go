// Package snapshot provides zero-downtime backups by leveraging S3/MinIO
// and RCON to sync/flush Spigot/Paper worlds to disk during live operations.
package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/rcon"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Options struct {
	Endpoint        string
	AccessKey       string
	SecretKey       string
	UseSSL          bool
	BucketName      string
	BackupPrefix    string // e.g. "backups/"
}

type Engine struct {
	s3     S3Options
	log    *slog.Logger
	client *minio.Client
}

func New(s3 S3Options, log *slog.Logger) (*Engine, error) {
	if log == nil {
		log = slog.Default()
	}
	client, err := minio.New(s3.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3.AccessKey, s3.SecretKey, ""),
		Secure: s3.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &Engine{
		s3:     s3,
		log:    log,
		client: client,
	}, nil
}

// BackupServer executes the live zero-downtime backup sequence:
// 1. Send "save-off" via RCON to disable auto-saving (avoids dirty reads)
// 2. Send "save-all flush" via RCON to write everything to disk
// 3. Compress the server data directory to a .tar.gz stream
// 4. Stream upload directly to S3
// 5. Send "save-on" via RCON to re-enable saving
func (e *Engine) BackupServer(ctx context.Context, serverID, dataDir, rconAddr, rconPassword string) (string, int64, error) {
	e.log.Info("starting zero-downtime backup", "server_id", serverID)

	// Attempt RCON save-off & flush (non-fatal if server is stopped/offline)
	rc, err := rcon.Dial(rconAddr, rconPassword)
	if err == nil {
		e.log.Debug("disabling auto-save via RCON", "server_id", serverID)
		_, _ = rc.Command("save-off")
		_, _ = rc.Command("save-all flush")
		defer func() {
			_, _ = rc.Command("save-on")
			rc.Close()
		}()
	} else {
		e.log.Warn("rcon unavailable; proceeding with normal backup", "server_id", serverID, "err", err)
	}

	// Create a pipe to stream tar.gz content directly to S3 without using local disk space
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	// Stream file compress in a goroutine
	go func() {
		defer pw.Close()
		gw := gzip.NewWriter(pw)
		defer gw.Close()
		tw := tar.NewWriter(gw)
		defer tw.Close()

		errChan <- tarDir(dataDir, tw)
	}()

	// Perform S3 upload
	objectKey := fmt.Sprintf("%s%s/%d.tar.gz", e.s3.BackupPrefix, serverID, time.Now().Unix())
	e.log.Info("streaming backup to S3", "server_id", serverID, "key", objectKey)

	// Ensure bucket exists
	exists, err := e.client.BucketExists(ctx, e.s3.BucketName)
	if err != nil {
		return "", 0, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		err = e.client.MakeBucket(ctx, e.s3.BucketName, minio.MakeBucketOptions{})
		if err != nil {
			return "", 0, fmt.Errorf("make bucket: %w", err)
		}
	}

	info, err := e.client.PutObject(ctx, e.s3.BucketName, objectKey, pr, -1, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return "", 0, fmt.Errorf("s3 put: %w", err)
	}

	// Check if tarring finished cleanly
	if tarErr := <-errChan; tarErr != nil {
		return "", 0, fmt.Errorf("tar failed: %w", tarErr)
	}

	e.log.Info("backup completed successfully", "server_id", serverID, "size_bytes", info.Size, "key", objectKey)
	return objectKey, info.Size, nil
}

func tarDir(srcDir string, tw *tar.Writer) error {
	return filepath.Walk(srcDir, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, file)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		header, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if fi.Mode().IsDir() {
			return nil
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}
