package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"matchcore/internal/config"
	"matchcore/internal/groups"
	"matchcore/internal/matching"
	"matchcore/internal/model"
	"matchcore/internal/redisstore"
	"matchcore/internal/serverquery"
)

type testQuery struct{}

func (testQuery) Query(_ context.Context, target serverquery.Target) (serverquery.Status, error) {
	if target.Name == "offline" {
		return serverquery.Status{}, errors.New("offline")
	}
	return serverquery.Status{Online: true, PlayerCount: 3, MaxPlayers: 8, MOTD: "State: 1"}, nil
}

func TestHTTPRedisMatchFlow(t *testing.T) {
	server := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "groups.yml")
	yaml := `servers:
  online: {address: "127.0.0.1:25565"}
  offline: {address: "127.0.0.1:25566"}
groups:
  solo: {method: redis, modes: [solo]}
  motd: {method: motd, servers: [online, offline], motd-patterns: ["State: 1"]}
`
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	rules, err := groups.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{RedisAddr: server.Addr(), RedisPoolSize: 4, RedisTimeout: 100 * time.Millisecond, RedisPrefix: "matchcore:", SnapshotTTL: 15 * time.Second, CollectTimeout: 5 * time.Second}
	client := redisstore.New(cfg)
	defer client.Close()
	for _, item := range []struct{ key, count, state string }{
		{"bedwars:solo:127.0.0.1:25565:arena1", "2", "WAITING"},
		{"bedwars:solo:127.0.0.2:25565:arena1", "5", "WAITING"},
		{"bedwars:solo:127.0.0.3:25565:full", "8", "WAITING"},
		{"bedwars:solo:127.0.0.4:25565:running", "7", "RUNNING"},
	} {
		server.HSet(item.key, "playerCount", item.count, "maxPlayers", "8", "state", item.state, "updatedAt", "1")
		server.SetTTL(item.key, 60*time.Second)
	}
	service := matching.New(cfg, rules, client, testQuery{})
	if err = service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	api := httptest.NewServer(New(token, time.Second, func(ctx context.Context) error { return client.Ping(ctx).Err() }, service))
	defer api.Close()
	request := func(method, path, body string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data
	}
	code, data := request(http.MethodGet, "/v1/groups", "")
	if code != 200 {
		t.Fatal("完整快照读取失败")
	}
	var snapshot model.Snapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.ConfigVersion != rules.Definition.ConfigVersion || len(snapshot.Groups) != 2 {
		t.Fatal("快照分组或版本异常")
	}
	for _, group := range snapshot.Groups {
		if group.Name == "solo" && len(group.Rooms) != 4 {
			t.Fatal("跨服同名地图被覆盖")
		}
		if group.Name == "motd" && len(group.Rooms) != 2 {
			t.Fatal("离线SLP源阻断了正常源")
		}
	}
	uuid := "00000000-0000-0000-0000-000000000001"
	code, data = request(http.MethodPost, "/v1/matches", `{"playerUuid":"`+uuid+`","group":"solo"}`)
	if code != 200 {
		t.Fatal("正常选房失败")
	}
	var ticket model.Ticket
	if err = json.Unmarshal(data, &ticket); err != nil {
		t.Fatal(err)
	}
	if ticket.Room.Address != "127.0.0.2:25565" || len(ticket.Token) != 64 || ticket.ExpiresAt-ticket.IssuedAt != 60000 {
		t.Fatal("优选、票据目标或时限不正确")
	}
	saved, err := server.Get("matchcore:ticket:" + uuid)
	if err != nil {
		t.Fatal("HTTP成功但Redis没有票据")
	}
	var stored model.Ticket
	if json.Unmarshal([]byte(saved), &stored) != nil || stored.Token != ticket.Token || server.TTL("matchcore:ticket:"+uuid) != 60*time.Second {
		t.Fatal("HTTP与Redis票据不一致")
	}
	code, data = request(http.MethodPost, "/v1/matches", `{"playerUuid":"`+uuid+`","group":"solo","arenaName":"full"}`)
	if code != 200 {
		t.Fatal("不可用指定地图未延续可用房间回退")
	}
	for _, item := range []struct {
		body string
		want int
	}{
		{`{"playerUuid":"bad","group":"solo"}`, 400},
		{`{"playerUuid":"` + uuid + `","group":"missing"}`, 404},
		{`{"playerUuid":"` + uuid + `","group":"solo","extra":true}`, 400},
	} {
		code, _ = request(http.MethodPost, "/v1/matches", item.body)
		if code != item.want {
			t.Fatal("错误请求状态码异常")
		}
	}
	for _, key := range server.Keys() {
		if strings.HasPrefix(key, "bedwars:") {
			server.HSet(key, "playerCount", "8")
		}
	}
	if err = service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, _ = request(http.MethodPost, "/v1/matches", `{"playerUuid":"`+uuid+`","group":"solo"}`)
	if code != 409 {
		t.Fatal("无房间未返回409")
	}
	server.Close()
	code, data = request(http.MethodPost, "/v1/matches", `{"playerUuid":"`+uuid+`","group":"motd"}`)
	if code != 503 || strings.Contains(string(data), "token") {
		t.Fatal("票据写失败未安全返回503")
	}
	if err = service.Refresh(context.Background()); err == nil {
		t.Fatal("Redis故障错误发布快照")
	}
	last, err := service.Snapshot()
	if err != nil || last.CollectedAt <= 0 {
		t.Fatal("失败采集破坏已有快照")
	}
}
