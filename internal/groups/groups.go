package groups

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"matchcore/internal/model"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Rules struct {
	Definition model.Definition
	motd       map[string][]*regexp.Regexp
	filters    map[string][]*regexp.Regexp
	excludes   map[string][]*regexp.Regexp
}

func Load(path string) (*Rules, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法读取分组文件")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("分组文件超过限制或读取失败")
	}
	var wen struct {
		Groups  map[string]model.Group  `yaml:"groups"`
		Servers map[string]model.Server `yaml:"servers"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&wen); err != nil {
		return nil, errors.New("分组文件格式错误")
	}
	var more any
	if err := dec.Decode(&more); err != io.EOF {
		return nil, errors.New("分组文件仅允许一份配置")
	}
	if len(wen.Groups) == 0 || len(wen.Groups) > 128 || len(wen.Servers) > 512 {
		return nil, errors.New("分组或服务器数量无效")
	}
	definition := model.Definition{SchemaVersion: model.SchemaVersion, TicketTTLMillis: model.TicketTTL.Milliseconds(), Groups: []model.Group{}, Servers: []model.Server{}}
	addresses := map[string]bool{}
	for name, server := range wen.Servers {
		if !ValidName(name) || !ValidAddress(server.Address) {
			return nil, errors.New("服务器名称或地址无效")
		}
		server.Name = name
		if addresses[server.Address] {
			return nil, errors.New("服务器地址不能重复配置")
		}
		addresses[server.Address] = true
		definition.Servers = append(definition.Servers, server)
	}
	for name, group := range wen.Groups {
		group.Name = name
		if !ValidName(name) {
			return nil, errors.New("分组名称无效")
		}
		if group.Mode != "" {
			if len(group.Modes) != 0 {
				return nil, errors.New("mode与modes只能配置一个")
			}
			group.Modes = []string{group.Mode}
		}
		group.Mode = ""
		if group.Method != "redis" && group.Method != "motd" {
			return nil, errors.New("分组method无效")
		}
		if len(group.JoinableStates) == 0 {
			if group.Method == "redis" {
				group.JoinableStates = []string{"WAITING"}
			} else {
				group.JoinableStates = []string{"ONLINE"}
			}
		}
		if group.Channel == "" && group.Method == "redis" {
			group.Channel = "bedwars:match"
		}
		if group.KeyPrefix == "" && group.Method == "redis" {
			group.KeyPrefix = "bedwars:"
		}
		if group.Method == "redis" && (len(group.Modes) == 0 || !ValidPrefix(group.KeyPrefix) || !validText(group.Channel, 128)) {
			return nil, errors.New("Redis分组配置无效")
		}
		if group.Method == "motd" && len(group.Servers) == 0 {
			return nil, errors.New("MOTD分组缺少服务器")
		}
		for _, serverName := range group.Servers {
			if _, ok := wen.Servers[serverName]; !ok {
				return nil, errors.New("分组引用了未知服务器")
			}
		}
		for _, value := range append(slices.Clone(group.Modes), group.JoinableStates...) {
			if !ValidName(value) {
				return nil, errors.New("模式或状态名称无效")
			}
		}
		if len(group.Modes)+len(group.JoinableStates)+len(group.ServerFilter)+len(group.ArenaExclude)+len(group.MOTDPatterns)+len(group.Servers) > 512 {
			return nil, errors.New("分组规则超过限制")
		}
		group.Modes = notNil(group.Modes)
		group.JoinableStates = notNil(group.JoinableStates)
		group.ServerFilter = notNil(group.ServerFilter)
		group.ArenaExclude = notNil(group.ArenaExclude)
		group.Servers = notNil(group.Servers)
		group.MOTDPatterns = notNil(group.MOTDPatterns)
		definition.Groups = append(definition.Groups, group)
	}
	sort.Slice(definition.Groups, func(i, j int) bool { return definition.Groups[i].Name < definition.Groups[j].Name })
	sort.Slice(definition.Servers, func(i, j int) bool { return definition.Servers[i].Name < definition.Servers[j].Name })
	data, _ = json.Marshal(definition)
	sum := sha256.Sum256(data)
	definition.ConfigVersion = hex.EncodeToString(sum[:])
	return Compile(definition)
}

func Compile(definition model.Definition) (*Rules, error) {
	rules := &Rules{Definition: definition, motd: make(map[string][]*regexp.Regexp), filters: make(map[string][]*regexp.Regexp), excludes: make(map[string][]*regexp.Regexp)}
	for _, group := range definition.Groups {
		for _, text := range group.MOTDPatterns {
			if len(text) > 2048 {
				return nil, errors.New("MOTD规则超过限制")
			}
			pattern, err := regexp.Compile("(?s)" + text)
			if err != nil {
				return nil, errors.New("MOTD规则不是可支持的正则")
			}
			rules.motd[group.Name] = append(rules.motd[group.Name], pattern)
		}
		for _, entry := range []struct {
			values []string
			dest   map[string][]*regexp.Regexp
		}{{group.ServerFilter, rules.filters}, {group.ArenaExclude, rules.excludes}} {
			for _, glob := range entry.values {
				if len(glob) == 0 || len(glob) > 256 {
					return nil, errors.New("glob规则长度无效")
				}
				text := strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, `.*`), `\?`, `.`)
				entry.dest[group.Name] = append(entry.dest[group.Name], regexp.MustCompile("^"+text+"$"))
			}
		}
	}
	return rules, nil
}

func (rules *Rules) Contains(group model.Group, room model.Room) bool {
	if group.Method != room.Method {
		return false
	}
	if group.Method == "motd" {
		return slices.Contains(group.Servers, room.ServerName)
	}
	return strings.HasPrefix(room.RoomID, group.KeyPrefix) && slices.Contains(group.Modes, room.Mode) &&
		(len(group.ServerFilter) == 0 || anyMatch(rules.filters[group.Name], room.Address)) && !anyMatch(rules.excludes[group.Name], room.ArenaName)
}

func (rules *Rules) Joinable(group model.Group, room model.Room, now int64) bool {
	if room.ExpiresAt <= now || room.PlayerCount < 0 || room.MaxPlayers <= 0 || room.PlayerCount >= room.MaxPlayers || !slices.Contains(group.JoinableStates, room.State) {
		return false
	}
	return room.Method != "motd" || len(group.MOTDPatterns) == 0 || anyMatch(rules.motd[group.Name], room.MOTD)
}

func (rules *Rules) Group(name string) (model.Group, bool) {
	for _, group := range rules.Definition.Groups {
		if group.Name == name {
			return group, true
		}
	}
	return model.Group{}, false
}

func (rules *Rules) ValidNamespace(prefix string) bool {
	if !ValidPrefix(prefix) {
		return false
	}
	for _, group := range rules.Definition.Groups {
		if group.Method == "redis" && (strings.HasPrefix(prefix, group.KeyPrefix) || strings.HasPrefix(group.KeyPrefix, prefix)) {
			return false
		}
	}
	return true
}

func ValidName(value string) bool { return namePattern.MatchString(value) }

func ValidAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || len(host) > 253 || strings.ContainsAny(host, " \t\r\n/@,") {
		return false
	}
	num, err := strconv.Atoi(port)
	return err == nil && num > 0 && num <= 65535
}

func ValidPrefix(value string) bool {
	return len(value) > 0 && len(value) <= 64 && strings.HasSuffix(value, ":") && regexp.MustCompile(`^[A-Za-z0-9:_-]+$`).MatchString(value)
}

func validText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && !strings.ContainsAny(value, "\r\n\x00,")
}
func notNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
func anyMatch(patterns []*regexp.Regexp, value string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}
