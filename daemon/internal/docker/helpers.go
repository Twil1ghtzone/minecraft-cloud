package docker

import (
	"bytes"
	"net"

	"github.com/docker/go-connections/nat"
)

// Aliases so the controller file doesn't import go-connections directly.
func convertExposed(in map[string]struct{}) nat.PortSet {
	out := make(nat.PortSet, len(in))
	for k := range in {
		out[nat.Port(k)] = struct{}{}
	}
	return out
}

func convertPortMap(in map[string][]container_PortBinding) nat.PortMap {
	out := make(nat.PortMap, len(in))
	for k, v := range in {
		bs := make([]nat.PortBinding, 0, len(v))
		for _, b := range v {
			bs = append(bs, nat.PortBinding{HostIP: b.HostIP, HostPort: b.HostPort})
		}
		out[nat.Port(k)] = bs
	}
	return out
}

// pickFreePort asks the kernel for an available TCP port by listening
// on :0 and immediately closing.
func pickFreePort() (uint32, error) {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

// bytesBuf is a tiny adapter so we can use bytes.Buffer without importing
// the package in controller.go (keeps imports tight there).
type bytesBuf struct{ bytes.Buffer }
