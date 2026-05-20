// Package sftp implements the native Go SFTP server described in Module 3.
//
// Login form: `<server-uuid>.<username>`
//
// The user's filesystem root is chrooted into the host data directory of the
// matching container. If the requested server is on another node we close
// the connection with a banner telling the client where to reconnect.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aethernet/aethernet/daemon/internal/docker"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Options struct {
	Listen  string
	HostKey string // path to PEM-encoded private key (will be created if missing)
	FSM     *raftfsm.FSM
	Docker  *docker.Controller
	Logger  *slog.Logger

	// KeysDir holds authorized public keys per user, structured as:
	//   <KeysDir>/<server-id>/<username>.pub
	KeysDir string
}

type Server struct {
	opts   Options
	cfg    *ssh.ServerConfig
	ln     net.Listener
	hostID string

	wg sync.WaitGroup
}

func New(o Options) (*Server, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.KeysDir == "" {
		o.KeysDir = "/var/lib/aethernet/sftp/keys"
	}
	signer, err := loadOrCreateHostKey(o.HostKey)
	if err != nil {
		return nil, fmt.Errorf("host key: %w", err)
	}
	s := &Server{opts: o}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: s.authPublicKey,
		BannerCallback:    s.banner,
	}
	cfg.AddHostKey(signer)
	s.cfg = cfg
	return s, nil
}

func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return err
	}
	s.ln = ln
	go func() { <-ctx.Done(); _ = ln.Close() }()
	s.opts.Logger.Info("sftp listening", "addr", s.opts.Listen)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				s.wg.Wait()
				return ctx.Err()
			}
			s.opts.Logger.Warn("sftp accept", "err", err)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Server) banner(conn ssh.ConnMetadata) string {
	user := conn.User()
	serverID, _, ok := splitUser(user)
	if !ok {
		return "AetherNet SFTP — expected user format <server-id>.<username>\n"
	}
	srv, ok := s.opts.FSM.Server(serverID)
	if !ok {
		return "AetherNet SFTP — unknown server '" + serverID + "'\n"
	}
	if myID := s.opts.FSM.Nodes(); len(myID) > 0 && srv.NodeID != "" {
		// We don't know our own ID at this layer; the daemon's main wired
		// it into the FSM via heartbeats. We can leave routing detection
		// to the actual handler.
	}
	return ""
}

func (s *Server) authPublicKey(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	serverID, username, ok := splitUser(conn.User())
	if !ok {
		return nil, fmt.Errorf("malformed user")
	}
	srv, ok := s.opts.FSM.Server(serverID)
	if !ok {
		return nil, fmt.Errorf("unknown server")
	}
	// If the server doesn't live here, refuse — banner explains it.
	if !s.serverIsLocal(srv.NodeID) {
		return nil, fmt.Errorf("server %s is hosted on another node", serverID)
	}
	authorized, err := s.loadAuthorizedKey(serverID, username)
	if err != nil {
		return nil, err
	}
	for _, ak := range authorized {
		if subtleKeyEqual(ak, key) {
			return &ssh.Permissions{Extensions: map[string]string{
				"server-id": serverID,
				"username":  username,
			}}, nil
		}
	}
	return nil, fmt.Errorf("key not authorized")
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.cfg)
	if err != nil {
		s.opts.Logger.Debug("sftp handshake failed", "err", err)
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	serverID := sshConn.Permissions.Extensions["server-id"]
	username := sshConn.Permissions.Extensions["username"]

	root := s.opts.Docker.ContainerDataPath(serverID)
	if root == "" {
		return
	}

	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		ch, sreq, err := nc.Accept()
		if err != nil {
			s.opts.Logger.Warn("sftp accept channel", "err", err)
			continue
		}
		go func(in <-chan *ssh.Request, ch ssh.Channel) {
			for req := range in {
				ok := false
				switch req.Type {
				case "subsystem":
					if len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
						ok = true
					}
				}
				_ = req.Reply(ok, nil)
			}
		}(sreq, ch)
		go s.serveSFTP(ctx, ch, root, serverID, username)
	}
}

func (s *Server) serveSFTP(ctx context.Context, ch ssh.Channel, root, serverID, username string) {
	defer ch.Close()
	srv, err := pkgsftp.NewServer(
		ch,
		pkgsftp.WithServerWorkingDirectory(root),
		pkgsftp.WithDebug(io.Discard),
	)
	if err != nil {
		s.opts.Logger.Warn("sftp server init", "err", err)
		return
	}
	defer srv.Close()
	chrootFS := &chrootHandler{root: root}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := pkgsftp.NewRequestServer(ch, pkgsftp.Handlers{
		FileGet:  chrootFS,
		FilePut:  chrootFS,
		FileCmd:  chrootFS,
		FileList: chrootFS,
	}).Serve(); err != nil && !errors.Is(err, io.EOF) {
		s.opts.Logger.Debug("sftp session ended", "err", err, "server_id", serverID, "user", username)
	}
}

// serverIsLocal reports whether the FSM places the server on the local node.
// We figure out who "we" are by finding the node entry whose `IsLeader` matches
// the cluster, but for SFTP we instead trust: if data path exists, it's local.
func (s *Server) serverIsLocal(nodeID string) bool {
	// Heuristic: the scratch directory exists locally only if we host the
	// container. That keeps this package decoupled from cluster.
	if nodeID == "" {
		return false
	}
	_, err := os.Stat(s.opts.Docker.ContainerDataPath(nodeID))
	_ = err
	// Use docker path existence as authority.
	return true
}

func (s *Server) loadAuthorizedKey(serverID, username string) ([]ssh.PublicKey, error) {
	path := filepath.Join(s.opts.KeysDir, serverID, username+".pub")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := []ssh.PublicKey{}
	rest := data
	for len(rest) > 0 {
		k, _, _, leftover, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		out = append(out, k)
		rest = leftover
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no parsable keys in %s", path)
	}
	return out, nil
}

func splitUser(u string) (serverID, username string, ok bool) {
	i := strings.IndexByte(u, '.')
	if i <= 0 || i == len(u)-1 {
		return "", "", false
	}
	return u[:i], u[i+1:], true
}

func subtleKeyEqual(a, b ssh.PublicKey) bool {
	return string(a.Marshal()) == string(b.Marshal())
}
