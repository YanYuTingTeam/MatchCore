package matching

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"matchcore/internal/config"
	"matchcore/internal/groups"
	"matchcore/internal/model"
	"matchcore/internal/serverquery"
)

var ErrData = errors.New("data_unavailable")
var ErrNoRoom = errors.New("no_room")
var ErrGroup = errors.New("group_not_found")
var ErrRequest = errors.New("bad_request")
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Service struct {
	cfg      config.Config
	rules    *groups.Rules
	redis    *redis.Client
	query    serverquery.Client
	mu       sync.RWMutex
	snapshot model.Snapshot
}

func New(cfg config.Config, rules *groups.Rules, client *redis.Client, query serverquery.Client) *Service {
	return &Service{cfg: cfg, rules: rules, redis: client, query: query}
}

func (service *Service) Refresh(ctx context.Context) error {
	if !service.rules.ValidNamespace(service.cfg.RedisPrefix) {
		return ErrData
	}
	ctx, cancel := context.WithTimeout(ctx, service.cfg.CollectTimeout)
	defer cancel()
	rooms, err := service.redisRooms(ctx)
	if err != nil {
		return err
	}
	rooms2, err := service.slpRooms(ctx)
	if err != nil {
		return err
	}
	rooms = append(rooms, rooms2...)
	now := time.Now().UnixMilli()
	snapshot := model.Snapshot{SchemaVersion: model.SchemaVersion, ConfigVersion: service.rules.Definition.ConfigVersion, CollectedAt: now, ExpiresAt: now + service.cfg.SnapshotTTL.Milliseconds(), Groups: []model.Group{}}
	totalRooms, totalBytes := 0, 0
	for _, group := range service.rules.Definition.Groups {
		group.Rooms = []model.Room{}
		for _, room := range rooms {
			if service.rules.Contains(group, room) && room.ExpiresAt > now {
				totalRooms++
				totalBytes += len(room.RoomID) + len(room.Address) + len(room.ArenaName) + len(room.MOTD) + len(room.Mode) + len(room.State) + len(room.ServerName) + len(group.Channel) + 256
				if totalRooms > 10000 || totalBytes > 4*1024*1024 {
					return ErrData
				}
				if room.Method == "redis" {
					room.Channel = group.Channel
				}
				group.Rooms = append(group.Rooms, room)
			}
		}
		sort.Slice(group.Rooms, func(i, j int) bool { return group.Rooms[i].RoomID < group.Rooms[j].RoomID })
		snapshot.Groups = append(snapshot.Groups, group)
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > 4*1024*1024 {
		return ErrData
	}
	definition, err := json.Marshal(service.rules.Definition)
	if err != nil || len(definition) > 2*1024*1024 {
		return ErrData
	}
	_, err = service.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, service.cfg.RedisPrefix+"config", definition, 0)
		pipe.Set(ctx, service.cfg.RedisPrefix+"snapshot", data, service.cfg.SnapshotTTL)
		return nil
	})
	if err != nil {
		return ErrData
	}
	service.mu.Lock()
	service.snapshot = snapshot
	service.mu.Unlock()
	return nil
}

func (service *Service) Snapshot() (model.Snapshot, error) {
	service.mu.RLock()
	snapshot := service.snapshot
	service.mu.RUnlock()
	if snapshot.SchemaVersion != model.SchemaVersion || snapshot.ExpiresAt <= time.Now().UnixMilli() {
		return model.Snapshot{}, ErrData
	}
	return snapshot, nil
}

func (service *Service) Match(ctx context.Context, request model.MatchRequest) (model.Ticket, error) {
	if !uuidPattern.MatchString(request.PlayerUUID) || !groups.ValidName(request.Group) || len(request.ArenaName) > 128 || strings.ContainsAny(request.ArenaName, ",\r\n\x00") {
		return model.Ticket{}, ErrRequest
	}
	group, ok := service.rules.Group(request.Group)
	if !ok {
		return model.Ticket{}, ErrGroup
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		return model.Ticket{}, ErrData
	}
	for _, item := range snapshot.Groups {
		if item.Name == group.Name {
			group = item
			break
		}
	}
	now := time.Now().UnixMilli()
	candidates := []model.Room{}
	preferred := []model.Room{}
	for _, room := range group.Rooms {
		if !service.rules.Joinable(group, room, now) {
			continue
		}
		candidates = append(candidates, room)
		if request.ArenaName != "" && (room.ArenaName == request.ArenaName || room.ServerName == request.ArenaName) {
			preferred = append(preferred, room)
		}
	}
	if len(preferred) > 0 {
		candidates = preferred
	}
	if len(candidates) == 0 {
		return model.Ticket{}, ErrNoRoom
	}
	maxPlayers := -1
	best := []model.Room{}
	for _, room := range candidates {
		if room.PlayerCount > maxPlayers {
			maxPlayers = room.PlayerCount
			best = best[:0]
		}
		if room.PlayerCount == maxPlayers {
			best = append(best, room)
		}
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(best))))
	if err != nil {
		return model.Ticket{}, ErrData
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		return model.Ticket{}, ErrData
	}
	ticket := model.Ticket{SchemaVersion: model.SchemaVersion, Token: hex.EncodeToString(token[:]), PlayerUUID: strings.ToLower(request.PlayerUUID), Group: group.Name, Room: best[index.Int64()], IssuedAt: now, ExpiresAt: now + model.TicketTTL.Milliseconds()}
	data, err := json.Marshal(ticket)
	if err != nil {
		return model.Ticket{}, ErrData
	}
	ctx, cancel := context.WithTimeout(ctx, service.cfg.RedisTimeout)
	defer cancel()
	if err = service.redis.Set(ctx, service.cfg.RedisPrefix+"ticket:"+ticket.PlayerUUID, data, model.TicketTTL).Err(); err != nil {
		return model.Ticket{}, ErrData
	}
	return ticket, nil
}

func (service *Service) redisRooms(ctx context.Context) ([]model.Room, error) {
	prefixes := []string{}
	seenPrefix := map[string]bool{}
	for _, group := range service.rules.Definition.Groups {
		if group.Method == "redis" && !seenPrefix[group.KeyPrefix] {
			prefixes = append(prefixes, group.KeyPrefix)
			seenPrefix[group.KeyPrefix] = true
		}
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i]) != len(prefixes[j]) {
			return len(prefixes[i]) > len(prefixes[j])
		}
		return prefixes[i] < prefixes[j]
	})
	seen := map[string]bool{}
	rooms := []model.Room{}
	pages := 0
	for _, prefix := range prefixes {
		cursor := uint64(0)
		for {
			pages++
			if pages > 1024 {
				return nil, ErrData
			}
			keys, next, err := service.redis.Scan(ctx, cursor, prefix+"*", 256).Result()
			if err != nil {
				return nil, ErrData
			}
			var batch []string
			for _, key := range keys {
				if seen[key] {
					continue
				}
				seen[key] = true
				if len(seen) > 10000 || len(key) > 1024 {
					return nil, ErrData
				}
				batch = append(batch, key)
			}
			if len(batch) > 0 {
				hashes := make([]*redis.SliceCmd, len(batch))
				ttls := make([]*redis.DurationCmd, len(batch))
				start := time.Now().UnixMilli()
				_, err = service.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
					for i, key := range batch {
						hashes[i] = pipe.HMGet(ctx, key, "playerCount", "maxPlayers", "state")
						ttls[i] = pipe.PTTL(ctx, key)
					}
					return nil
				})
				if err != nil {
					return nil, ErrData
				}
				for i, key := range batch {
					values, err := hashes[i].Result()
					if err != nil {
						return nil, ErrData
					}
					ttl, err := ttls[i].Result()
					if err != nil {
						return nil, ErrData
					}
					if ttl <= 0 || len(values) != 3 {
						continue
					}
					fields := map[string]string{}
					valid := true
					for j, name := range []string{"playerCount", "maxPlayers", "state"} {
						value, ok := values[j].(string)
						if !ok || len(value) > 64 {
							valid = false
							break
						}
						fields[name] = value
					}
					if !valid {
						continue
					}
					room, ok := parseRoom(prefix, key, fields)
					if !ok {
						continue
					}
					room.CollectedAt = time.Now().UnixMilli()
					room.ExpiresAt = min(start+ttl.Milliseconds(), room.CollectedAt+service.cfg.SnapshotTTL.Milliseconds())
					if room.ExpiresAt > room.CollectedAt {
						rooms = append(rooms, room)
					}
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return rooms, nil
}

func parseRoom(prefix, key string, fields map[string]string) (model.Room, bool) {
	suffix := strings.TrimPrefix(key, prefix)
	mode, tail, ok := strings.Cut(suffix, ":")
	if !ok || !groups.ValidName(mode) {
		return model.Room{}, false
	}
	var host string
	if strings.HasPrefix(tail, "[") {
		end := strings.Index(tail, "]:")
		if end < 0 {
			return model.Room{}, false
		}
		host = tail[:end+1]
		tail = tail[end+2:]
	} else {
		host, tail, ok = strings.Cut(tail, ":")
		if !ok {
			return model.Room{}, false
		}
	}
	port, arena, ok := strings.Cut(tail, ":")
	address := host + ":" + port
	if !ok || !groups.ValidAddress(address) || arena == "" || len(arena) > 128 || strings.ContainsAny(arena, ",\r\n\x00") {
		return model.Room{}, false
	}
	count, err1 := strconv.Atoi(fields["playerCount"])
	maxPlayers, err2 := strconv.Atoi(fields["maxPlayers"])
	if err1 != nil || err2 != nil || count < 0 || count > 2147483647 || maxPlayers <= 0 || maxPlayers > 2147483647 || !groups.ValidName(fields["state"]) {
		return model.Room{}, false
	}
	return model.Room{RoomID: key, Method: "redis", Address: address, ArenaName: arena, Mode: mode, PlayerCount: count, MaxPlayers: maxPlayers, State: fields["state"]}, true
}

func (service *Service) slpRooms(ctx context.Context) ([]model.Room, error) {
	used := map[string]bool{}
	for _, group := range service.rules.Definition.Groups {
		if group.Method == "motd" {
			for _, name := range group.Servers {
				used[name] = true
			}
		}
	}
	servers := []model.Server{}
	for _, server := range service.rules.Definition.Servers {
		if used[server.Name] {
			servers = append(servers, server)
		}
	}
	jobs := make(chan model.Server, len(servers))
	results := make(chan model.Room, len(servers))
	for _, server := range servers {
		jobs <- server
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(8, len(servers)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for server := range jobs {
				status, err := service.query.Query(ctx, serverquery.Target{Name: server.Name, Address: server.Address})
				now := time.Now().UnixMilli()
				room := model.Room{RoomID: "motd:" + server.Address, Method: "motd", Address: server.Address, ServerName: server.Name, State: "OFFLINE", CollectedAt: now, ExpiresAt: now + service.cfg.SnapshotTTL.Milliseconds()}
				if err == nil && status.Online {
					room.State = "ONLINE"
					room.PlayerCount = status.PlayerCount
					room.MaxPlayers = status.MaxPlayers
					room.MOTD = status.MOTD
				}
				results <- room
			}
		}()
	}
	workers.Wait()
	close(results)
	if ctx.Err() != nil {
		return nil, ErrData
	}
	rooms := []model.Room{}
	for room := range results {
		rooms = append(rooms, room)
	}
	return rooms, nil
}

func ValidUUID(value string) bool { return uuidPattern.MatchString(value) }
