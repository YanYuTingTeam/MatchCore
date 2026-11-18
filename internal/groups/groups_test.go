package groups

import (
	"matchcore/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestRulesAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.yml")
	data := `servers:
  bw-a:
    address: "127.0.0.1:25565"
groups:
  solo:
    method: redis
    mode: solo
    server-filter: ["127.0.0.*:25565"]
    arena-exclude: ["test*"]
  motd:
    method: motd
    servers: [bw-a]
    motd-patterns: ["State: 1.*Map: arena1"]
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	rules, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(path)
	if err != nil || rules.Definition.ConfigVersion != second.Definition.ConfigVersion {
		t.Fatal("分组版本不稳定")
	}
	solo, _ := rules.Group("solo")
	room := model.Room{RoomID: "bedwars:solo:127.0.0.1:25565:arena1", Method: "redis", Mode: "solo", Address: "127.0.0.1:25565", ArenaName: "arena1", State: "WAITING", PlayerCount: 2, MaxPlayers: 8, ExpiresAt: 1000}
	if !rules.Contains(solo, room) || !rules.Joinable(solo, room, 1) {
		t.Fatal("正常房间不可加入")
	}
	room.ArenaName = "testMap"
	if rules.Contains(solo, room) {
		t.Fatal("排除地图仍被选择")
	}
	room.ArenaName = "arena1"
	room.Address = "127x0x0x1:25565"
	if rules.Contains(solo, room) {
		t.Fatal("glob字面点被当作正则")
	}
	motd, _ := rules.Group("motd")
	room = model.Room{Method: "motd", ServerName: "bw-a", State: "ONLINE", MOTD: "State: 1\nMap: arena1", PlayerCount: 2, MaxPlayers: 8, ExpiresAt: 1000}
	if !rules.Contains(motd, room) || !rules.Joinable(motd, room, 1) {
		t.Fatal("MOTD跨行匹配失败")
	}
	room.PlayerCount = 8
	if rules.Joinable(motd, room, 1) {
		t.Fatal("满房被选择")
	}
	room.PlayerCount = 2
	if rules.Joinable(motd, room, 1000) {
		t.Fatal("过期房间被选择")
	}
}

func TestBadGroups(t *testing.T) {
	for _, data := range []string{
		`groups: {solo: {method: redis, mode: solo, modes: [solo]}}`,
		`groups: {solo: {method: redis, mode: solo, unknown: true}}`,
		`groups: {motd: {method: motd, servers: [missing]}}`,
		`groups: {motd: {method: motd, servers: [a], motd-patterns: ['(?<=x)y']}}
servers: {a: {address: '127.0.0.1:25565'}}`,
		`groups: {solo: {method: redis, mode: solo, key-prefix: '*'}}`,
	} {
		path := filepath.Join(t.TempDir(), "groups.yml")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("错误分组未被拒绝")
		}
	}
}
