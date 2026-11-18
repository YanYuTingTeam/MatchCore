package matching

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"matchcore/internal/config"
	"matchcore/internal/groups"
	"matchcore/internal/model"
	"matchcore/internal/redisstore"
	"matchcore/internal/serverquery"
)

type noSLP struct{}

func (noSLP) Query(context.Context, serverquery.Target) (serverquery.Status, error) {
	return serverquery.Status{}, errors.New("unused")
}

func TestExpiryBudgetAndNamespace(t *testing.T) {
	server := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "groups.yml")
	if err := os.WriteFile(path, []byte("groups:\n  solo: {method: redis, mode: solo}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rules, err := groups.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{RedisAddr: server.Addr(), RedisPoolSize: 4, RedisTimeout: time.Second, RedisPrefix: "matchcore:", SnapshotTTL: 15 * time.Second, CollectTimeout: 30 * time.Second}
	client := redisstore.New(cfg)
	defer client.Close()
	service := New(cfg, rules, client, noSLP{})
	key := "bedwars:solo:127.0.0.1:25565:arena1"
	server.HSet(key, "playerCount", "1", "maxPlayers", "8", "state", "WAITING", "updatedAt", "1")
	server.SetTTL(key, time.Second)
	if err = service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.FastForward(2 * time.Second)
	if err = service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := model.MatchRequest{PlayerUUID: "00000000-0000-0000-0000-000000000001", Group: "solo"}
	if _, err = service.Match(context.Background(), request); !errors.Is(err, ErrNoRoom) {
		t.Fatal("正常失效Hash仍可加入")
	}
	before, _ := service.Snapshot()
	for i := 0; i < 10001; i++ {
		key = fmt.Sprintf("bedwars:solo:127.0.0.1:25565:budget%d", i)
		server.HSet(key, "playerCount", "1", "maxPlayers", "8", "state", "WAITING")
		server.SetTTL(key, time.Minute)
	}
	if err = service.Refresh(context.Background()); !errors.Is(err, ErrData) {
		t.Fatal("超预算扫描发布了部分快照")
	}
	after, _ := service.Snapshot()
	if before.CollectedAt != after.CollectedAt {
		t.Fatal("失败扫描刷新了旧快照时限")
	}
	badCfg := cfg
	badCfg.RedisPrefix = "bedwars:"
	if err = New(badCfg, rules, client, noSLP{}).Refresh(context.Background()); !errors.Is(err, ErrData) {
		t.Fatal("危险命名空间未拒绝")
	}
	service.mu.Lock()
	service.snapshot.ExpiresAt = time.Now().UnixMilli() - 1
	service.mu.Unlock()
	if _, err = service.Match(context.Background(), request); !errors.Is(err, ErrData) {
		t.Fatal("过期快照仍可匹配")
	}
}
