package database

import "github.com/aethernet/aethernet/pkg/types"

// DBInfo is a lightweight copy of types.Database used by the API layer
// without depending on the full types package in the handler.
type DBInfo struct {
	ID       string
	Name     string
	Username string
}

// DBInfoToTypes converts DBInfo to types.Database for Workbench calls.
func DBInfoToTypes(d DBInfo) types.Database {
	return types.Database{
		ID:       d.ID,
		Name:     d.Name,
		Username: d.Username,
	}
}
