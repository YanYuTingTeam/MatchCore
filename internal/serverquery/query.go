package serverquery

import (
	"context"
	"time"
)

type Target struct {
	Name    string
	Address string
}

type Status struct {
	Online      bool
	PlayerCount int
	MaxPlayers  int
	MOTD        string
	CollectedAt time.Time
}

type Client interface {
	Query(context.Context, Target) (Status, error)
}
