package api

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
)

// ---- service stubs ---------------------------------------------------------
//
// In a fully wired build these implement the generated gRPC server
// interfaces from pkg/proto/{cluster,daemon,server,database,velocity}.
// Until `make proto` runs they are kept as thin shells so the daemon
// compiles. The real method bodies are below in heartbeat.go etc.

func registerGRPCServices(s *grpc.Server, o Options) {
	// Each ServiceDesc lives in its own file once protos are generated:
	//   clusterv1.RegisterClusterServiceServer(s, &clusterGRPC{opts: o})
	//   daemonv1.RegisterDaemonServiceServer(s,   &daemonGRPC{opts: o})
	//   databasev1.RegisterDatabaseServiceServer(s,&dbGRPC{opts: o})
	//   velocityv1.RegisterVelocityServiceServer(s,&velocityGRPC{opts: o})
	_ = o
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
	// Once protos are generated:
	//   client := clusterv1.NewClusterServiceClient(conn)
	//   _, err = client.Join(dctx, &clusterv1.JoinRequest{
	//       NodeId:    o.Cluster.LocalID(),
	//       Address:   o.Cluster.AdvertiseAddr(),
	//       JoinToken: token,
	//   })
	// For now we just succeed so the daemon can boot in single-node mode.
	_ = token
	return nil
}
