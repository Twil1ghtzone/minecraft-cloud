package raftfsm

import (
	"encoding/json"
	"fmt"
)

// CommandType enumerates every mutation kind committed to the FSM.
type CommandType string

const (
	CmdNodeUpsert      CommandType = "node.upsert"
	CmdNodeRemove      CommandType = "node.remove"
	CmdServerUpsert    CommandType = "server.upsert"
	CmdServerRemove    CommandType = "server.remove"
	CmdServerSetState  CommandType = "server.set_state"
	CmdServerSetHost   CommandType = "server.set_host"
	CmdGroupUpsert     CommandType = "group.upsert"
	CmdGroupRemove     CommandType = "group.remove"
	CmdTemplateUpsert  CommandType = "template.upsert"
	CmdTemplateRemove  CommandType = "template.remove"
	CmdDatabaseUpsert  CommandType = "database.upsert"
	CmdDatabaseRemove  CommandType = "database.remove"
	CmdTokenUpsert     CommandType = "token.upsert"
	CmdTokenRemove     CommandType = "token.remove"
	CmdHeartbeat       CommandType = "node.heartbeat"
)

// Command is the on-the-wire envelope persisted to the Raft log.
type Command struct {
	Type      CommandType     `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Idem      string          `json:"idem,omitempty"` // idempotency key
	Timestamp int64           `json:"ts"`
}

func Encode(t CommandType, payload any, idem string, ts int64) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	return json.Marshal(Command{Type: t, Payload: raw, Idem: idem, Timestamp: ts})
}

func Decode(b []byte) (Command, error) {
	var c Command
	if err := json.Unmarshal(b, &c); err != nil {
		return Command{}, err
	}
	return c, nil
}
