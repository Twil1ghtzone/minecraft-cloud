// Package tasks implements the CloudNet-style "tasks & groups" abstraction:
//
//   - A Group is a logical service (e.g. "lobby", "skyblock", "minigame-bedwars").
//   - Each Group references a Template.
//   - Each Group has MinInstances / MaxInstances; the scheduler keeps the
//     live count between those bounds. When player count drops, instances
//     scale down; when it grows, more spawn.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/cluster"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/pkg/types"
)

type Service struct {
	Cluster *cluster.Cluster
	FSM     *raftfsm.FSM
}

func (s *Service) CreateGroup(ctx context.Context, g types.Group) error {
	if g.ID == "" {
		g.ID = "grp_" + g.Name
	}
	if g.MinInstances < 0 || g.MaxInstances < 0 {
		return errors.New("min/max instances must be non-negative")
	}
	if g.MaxInstances != 0 && g.MaxInstances < g.MinInstances {
		return fmt.Errorf("max (%d) must be >= min (%d)", g.MaxInstances, g.MinInstances)
	}
	if _, ok := s.FSM.Template(g.TemplateID); !ok {
		return fmt.Errorf("template %q not found", g.TemplateID)
	}
	data, err := raftfsm.Encode(raftfsm.CmdGroupUpsert, g, "", time.Now().UnixNano())
	if err != nil {
		return err
	}
	return s.Cluster.Apply(ctx, data, 5*time.Second)
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	// Stop all servers in the group first.
	for _, srv := range s.FSM.Servers() {
		if srv.Spec.GroupID == id {
			srv.State = types.ServerStopping
			data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
			_ = s.Cluster.Apply(ctx, data, 2*time.Second)
		}
	}
	data, _ := raftfsm.Encode(raftfsm.CmdGroupRemove, map[string]string{"id": id}, "", time.Now().UnixNano())
	return s.Cluster.Apply(ctx, data, 5*time.Second)
}

func (s *Service) CreateTemplate(ctx context.Context, t types.Template) error {
	if t.ID == "" {
		t.ID = "tpl_" + t.Name
	}
	if t.Mode != "static" && t.Mode != "dynamic" {
		return fmt.Errorf("template mode must be static|dynamic, got %q", t.Mode)
	}
	data, err := raftfsm.Encode(raftfsm.CmdTemplateUpsert, t, "", time.Now().UnixNano())
	if err != nil {
		return err
	}
	return s.Cluster.Apply(ctx, data, 5*time.Second)
}

func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	// Refuse if any group references the template.
	for _, g := range s.FSM.Groups() {
		if g.TemplateID == id {
			return fmt.Errorf("template %q is in use by group %q", id, g.ID)
		}
	}
	data, _ := raftfsm.Encode(raftfsm.CmdTemplateRemove, map[string]string{"id": id}, "", time.Now().UnixNano())
	return s.Cluster.Apply(ctx, data, 5*time.Second)
}
