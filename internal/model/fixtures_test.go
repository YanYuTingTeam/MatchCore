package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolFixtures(t *testing.T) {
	read := func(name string, out any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	var definition Definition
	var snapshot Snapshot
	var ticket Ticket
	read("config.json", &definition)
	read("snapshot.json", &snapshot)
	read("ticket.json", &ticket)
	if definition.SchemaVersion != SchemaVersion || snapshot.ConfigVersion != definition.ConfigVersion || len(definition.ConfigVersion) != 64 || ticket.SchemaVersion != SchemaVersion || ticket.ExpiresAt-ticket.IssuedAt != TicketTTL.Milliseconds() {
		t.Fatal("共享结构/版本/毫秒时限不一致")
	}
	rooms := map[string]Room{}
	sameName := 0
	for _, group := range snapshot.Groups {
		for _, room := range group.Rooms {
			rooms[room.RoomID] = room
			if room.ArenaName == "arena1" {
				sameName++
			}
		}
	}
	if len(rooms) != 2 || sameName != 2 || rooms[ticket.Room.RoomID].Address != ticket.Room.Address || ticket.Room.Address != "127.0.0.2:25565" {
		t.Fatal("同名跨服或票据目标字段不一致")
	}
}
