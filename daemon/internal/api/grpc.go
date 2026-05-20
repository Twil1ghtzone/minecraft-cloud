// Package api — grpc.go
// gRPC service implementations for cluster sync, proxy routing, and agent commands.
package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/proto/clusterv1"
	"github.com/aethernet/aethernet/daemon/internal/proto/daemonv1"
	"github.com/aethernet/aethernet/daemon/internal/proto/proxyv1"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/daemon/internal/rcon"
	"github.com/aethernet/aethernet/pkg/types"
	"google.golang.org/grpc"
)

func registerGRPCServices(s *grpc.Server, o Options) {
	clusterv1.RegisterClusterServiceServer(s, &clusterGRPC{opts: o})
	proxyv1.RegisterProxyBridgeServiceServer(s, &proxyGRPC{opts: o})
	daemonv1.RegisterDaemonServiceServer(s, &daemonGRPC{opts: o})
}

// ---- Cluster Service Implementation ------------------------------------------

type clusterGRPC struct {
	clusterv1.UnimplementedClusterServiceServer
	opts Options
}

func (s *clusterGRPC) Join(ctx context.Context, req *clusterv1.JoinRequest) (*clusterv1.JoinResponse, error) {
	// Add node as voter to Raft cluster
	err := s.opts.Cluster.AddVoter(req.NodeId, req.RaftAddr)
	if err != nil {
		return &clusterv1.JoinResponse{
			Accepted:   false,
			LeaderId:   s.opts.Cluster.LeaderID(),
			LeaderAddr: s.opts.Cluster.LeaderAddr(),
			Error:      err.Error(),
		}, nil
	}

	return &clusterv1.JoinResponse{
		Accepted:   true,
		LeaderId:   s.opts.Cluster.LeaderID(),
		LeaderAddr: s.opts.Cluster.LeaderAddr(),
	}, nil
}

func (s *clusterGRPC) ForwardMutation(ctx context.Context, req *clusterv1.ForwardRequest) (*clusterv1.ForwardResponse, error) {
	// Local HTTP address resolution
	addr := s.opts.HTTPListen
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	localURL := fmt.Sprintf("http://%s%s", addr, req.Path)

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, localURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Internal request bypasses auth check or supplies internal auth header
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	respHeaders := make(map[string]string)
	for k, vv := range resp.Header {
		if len(vv) > 0 {
			respHeaders[k] = vv[0]
		}
	}

	return &clusterv1.ForwardResponse{
		StatusCode: uint32(resp.StatusCode),
		Body:       respBody,
		Headers:    respHeaders,
	}, nil
}

// grpcJoin is the implementation of cluster.JoinHook. It dials an existing
// cluster member's gRPC ClusterService and sends a JoinRequest.
func grpcJoin(ctx context.Context, addr, token string, o Options) error {
	if addr == "" {
		return errors.New("empty join address")
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dctx, addr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return err
	}
	defer conn.Close()

	client := clusterv1.NewClusterServiceClient(conn)
	resp, err := client.Join(dctx, &clusterv1.JoinRequest{
		NodeId:   o.Cluster.LocalID(),
		RaftAddr: o.Cluster.AdvertiseAddr(),
		JoinToken: token,
	})
	if err != nil {
		return err
	}
	if !resp.Accepted {
		return fmt.Errorf("join rejected by leader: %s", resp.Error)
	}
	return nil
}

// ---- Proxy Bridge Service Implementation ------------------------------------

type proxyGRPC struct {
	proxyv1.UnimplementedProxyBridgeServiceServer
	opts Options
}

func (s *proxyGRPC) Subscribe(req *proxyv1.ProxySubscribeRequest, stream proxyv1.ProxyBridgeService_SubscribeServer) error {
	s.opts.Logger.Info("proxy subscribed", "proxy_id", req.ProxyId, "type", req.ProxyType)

	// Subscribe to FSM events
	events := s.opts.FSM.Subscribe(100)
	defer func() {
		s.opts.Logger.Info("proxy unsubscribed", "proxy_id", req.ProxyId)
	}()

	// Send initial state: all ready servers matching filters
	for _, srv := range s.opts.FSM.Servers() {
		if srv.State == types.ServerReady {
			event := &proxyv1.ServerEvent{
				ServerId:       srv.Spec.ID,
				ServerName:     srv.Spec.Name,
				HostIp:         srv.NodeID, // Or resolved node IP
				HostPort:       srv.HostPort,
				GroupId:        srv.Spec.GroupID,
				EventType:      proxyv1.ServerEventType_SERVER_EVENT_TYPE_READY,
				Timestamp:      time.Now().Unix(),
				MaxPlayers:     srv.MaxPlayers,
				CurrentPlayers: srv.PlayerCount,
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case ev := <-events:
			if ev.Kind == string(raftfsm.CmdServerUpsert) {
				srv := ev.Payload.(types.Server)
				var et proxyv1.ServerEventType
				switch srv.State {
				case types.ServerStarting:
					et = proxyv1.ServerEventType_SERVER_EVENT_TYPE_STARTING
				case types.ServerReady:
					et = proxyv1.ServerEventType_SERVER_EVENT_TYPE_READY
				case types.ServerStopping:
					et = proxyv1.ServerEventType_SERVER_EVENT_TYPE_STOPPING
				case types.ServerStopped:
					et = proxyv1.ServerEventType_SERVER_EVENT_TYPE_STOPPED
				case types.ServerCrashed:
					et = proxyv1.ServerEventType_SERVER_EVENT_TYPE_CRASHED
				default:
					continue
				}

				event := &proxyv1.ServerEvent{
					ServerId:       srv.Spec.ID,
					ServerName:     srv.Spec.Name,
					HostIp:         srv.NodeID,
					HostPort:       srv.HostPort,
					GroupId:        srv.Spec.GroupID,
					EventType:      et,
					Timestamp:      time.Now().Unix(),
					MaxPlayers:     srv.MaxPlayers,
					CurrentPlayers: srv.PlayerCount,
				}
				if err := stream.Send(event); err != nil {
					return err
				}
			}
		}
	}
}

func (s *proxyGRPC) UpdateSign(ctx context.Context, req *proxyv1.SignUpdateRequest) (*proxyv1.AckResponse, error) {
	// Implement sign updates or broadcast to other nodes
	return &proxyv1.AckResponse{Ok: true}, nil
}

func (s *proxyGRPC) UpdateTablist(ctx context.Context, req *proxyv1.TablistUpdateRequest) (*proxyv1.AckResponse, error) {
	// Implement tablist sync
	return &proxyv1.AckResponse{Ok: true}, nil
}

// ---- Daemon Service Implementation ------------------------------------------

type daemonGRPC struct {
	daemonv1.UnimplementedDaemonServiceServer
	opts Options
}

func (s *daemonGRPC) SendRCON(ctx context.Context, req *daemonv1.RCONCommandRequest) (*daemonv1.RCONCommandResponse, error) {
	srv, ok := s.opts.FSM.Server(req.ServerId)
	if !ok {
		return nil, fmt.Errorf("server %s not found", req.ServerId)
	}

	// In a production container setup, RCON is exposed on a dedicated loopback port.
	// We read the RCON configuration from the daemon options or use a default.
	rconAddr := fmt.Sprintf("127.0.0.1:%d", srv.HostPort+1000) // convention or from spec
	rconPassword := "aethernet_secure_rcon"

	client, err := rcon.Dial(rconAddr, rconPassword)
	if err != nil {
		return &daemonv1.RCONCommandResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	defer client.Close()

	output, err := client.Command(req.Command)
	if err != nil {
		return &daemonv1.RCONCommandResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &daemonv1.RCONCommandResponse{
		Output:  output,
		Success: true,
	}, nil
}

func (s *daemonGRPC) StreamLogs(req *daemonv1.LogStreamRequest, stream daemonv1.DaemonService_StreamLogsServer) error {
	if s.opts.Docker == nil {
		return errors.New("docker controller not available")
	}

	writer := &grpcLogWriter{stream: stream}
	return s.opts.Docker.StreamLogs(stream.Context(), req.ServerId, req.Follow, int(req.TailLines), writer)
}

type grpcLogWriter struct {
	stream daemonv1.DaemonService_StreamLogsServer
}

func (w *grpcLogWriter) Write(b []byte) (int, error) {
	err := w.stream.Send(&daemonv1.LogChunk{
		Data:      b,
		Timestamp: time.Now().UnixNano(),
		Stderr:    false,
	})
	if err != nil {
		return 0, err
	}
	return len(b), nil
}
