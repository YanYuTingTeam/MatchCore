package model

import "time"

const SchemaVersion = 1
const TicketTTL = 60 * time.Second

type Server struct {
	Name    string `json:"name" yaml:"-"`
	Address string `json:"address" yaml:"address"`
}

type Room struct {
	RoomID      string `json:"roomId"`
	Method      string `json:"method"`
	Address     string `json:"address"`
	ServerName  string `json:"serverName"`
	ArenaName   string `json:"arenaName"`
	Mode        string `json:"mode"`
	Channel     string `json:"channel"`
	PlayerCount int    `json:"playerCount"`
	MaxPlayers  int    `json:"maxPlayers"`
	State       string `json:"state"`
	MOTD        string `json:"motd"`
	CollectedAt int64  `json:"collectedAt"`
	ExpiresAt   int64  `json:"expiresAt"`
}

type Group struct {
	Name           string   `json:"name" yaml:"-"`
	Method         string   `json:"method" yaml:"method"`
	Mode           string   `json:"-" yaml:"mode,omitempty"`
	Modes          []string `json:"modes" yaml:"modes"`
	JoinableStates []string `json:"joinableStates" yaml:"joinable-states"`
	Channel        string   `json:"channel" yaml:"channel"`
	KeyPrefix      string   `json:"keyPrefix" yaml:"key-prefix"`
	ServerFilter   []string `json:"serverFilter" yaml:"server-filter"`
	ArenaExclude   []string `json:"arenaExclude" yaml:"arena-exclude"`
	Servers        []string `json:"servers" yaml:"servers"`
	MOTDPatterns   []string `json:"motdPatterns" yaml:"motd-patterns"`
	Rooms          []Room   `json:"rooms,omitempty" yaml:"-"`
}

type Definition struct {
	SchemaVersion   int      `json:"schemaVersion"`
	ConfigVersion   string   `json:"configVersion"`
	TicketTTLMillis int64    `json:"ticketTtlMillis"`
	Groups          []Group  `json:"groups"`
	Servers         []Server `json:"servers"`
}

type Snapshot struct {
	SchemaVersion int     `json:"schemaVersion"`
	ConfigVersion string  `json:"configVersion"`
	CollectedAt   int64   `json:"collectedAt"`
	ExpiresAt     int64   `json:"expiresAt"`
	Groups        []Group `json:"groups"`
}

type Ticket struct {
	SchemaVersion int    `json:"schemaVersion"`
	Token         string `json:"token"`
	PlayerUUID    string `json:"playerUuid"`
	Group         string `json:"group"`
	Room          Room   `json:"room"`
	IssuedAt      int64  `json:"issuedAt"`
	ExpiresAt     int64  `json:"expiresAt"`
}

type MatchRequest struct {
	PlayerUUID string `json:"playerUuid"`
	Group      string `json:"group"`
	ArenaName  string `json:"arenaName,omitempty"`
}
