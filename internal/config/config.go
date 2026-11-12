package config

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr        string
	APIToken        string
	RedisAddr       string
	RedisUsername   string
	RedisPassword   string
	RedisDB         int
	RedisTLS        bool
	RedisPoolSize   int
	RedisTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type fileConfig struct {
	HTTPAddr        string
	RedisAddr       string
	RedisUsername   string
	RedisDB         int
	RedisTLS        bool
	RedisPoolSize   int
	RedisTimeout    string
	ShutdownTimeout string
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, errors.New("无法读取配置文件")
	}
	if len(data) > 64*1024 {
		return Config{}, errors.New("配置文件超过 64 KiB")
	}
	values, err := godotenv.Unmarshal(string(data))
	if err != nil {
		return Config{}, errors.New("配置文件 dotenv 格式错误")
	}
	known := map[string]bool{
		"MATCHCORE_HTTP_ADDR": true, "MATCHCORE_API_TOKEN": true,
		"MATCHCORE_REDIS_ADDR": true, "MATCHCORE_REDIS_USERNAME": true,
		"MATCHCORE_REDIS_PASSWORD": true, "MATCHCORE_REDIS_DB": true,
		"MATCHCORE_REDIS_TLS": true, "MATCHCORE_REDIS_POOL_SIZE": true,
		"MATCHCORE_REDIS_TIMEOUT": true, "MATCHCORE_SHUTDOWN_TIMEOUT": true,
	}
	for key := range values {
		if !known[key] {
			return Config{}, errors.New("配置文件包含未知配置项")
		}
	}
	get := func(key, fallback string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		if value, ok := values[key]; ok {
			return value
		}
		return fallback
	}
	wen := fileConfig{
		HTTPAddr:        get("MATCHCORE_HTTP_ADDR", "127.0.0.1:8080"),
		RedisAddr:       get("MATCHCORE_REDIS_ADDR", "127.0.0.1:6379"),
		RedisUsername:   get("MATCHCORE_REDIS_USERNAME", ""),
		RedisTimeout:    get("MATCHCORE_REDIS_TIMEOUT", "2s"),
		ShutdownTimeout: get("MATCHCORE_SHUTDOWN_TIMEOUT", "10s"),
	}
	wen.RedisDB, err = strconv.Atoi(get("MATCHCORE_REDIS_DB", "0"))
	if err != nil {
		return Config{}, errors.New("MATCHCORE_REDIS_DB 必须为整数")
	}
	wen.RedisPoolSize, err = strconv.Atoi(get("MATCHCORE_REDIS_POOL_SIZE", "10"))
	if err != nil {
		return Config{}, errors.New("MATCHCORE_REDIS_POOL_SIZE 必须为整数")
	}
	wen.RedisTLS, err = strconv.ParseBool(get("MATCHCORE_REDIS_TLS", "false"))
	if err != nil {
		return Config{}, errors.New("MATCHCORE_REDIS_TLS 必须为布尔值")
	}
	if !validAddr(wen.HTTPAddr, true) || !validAddr(wen.RedisAddr, false) {
		return Config{}, errors.New("MATCHCORE_HTTP_ADDR 和 MATCHCORE_REDIS_ADDR 必须为有效的 host:port")
	}
	if wen.RedisDB < 0 || wen.RedisPoolSize < 1 || wen.RedisPoolSize > 128 {
		return Config{}, errors.New("MATCHCORE_REDIS_DB 不得小于 0, MATCHCORE_REDIS_POOL_SIZE 必须为 1 到 128")
	}
	redisWait, err := time.ParseDuration(wen.RedisTimeout)
	if err != nil || redisWait < 100*time.Millisecond || redisWait > 5*time.Second {
		return Config{}, errors.New("MATCHCORE_REDIS_TIMEOUT 必须为 100ms 到 5s")
	}
	stopWait, err := time.ParseDuration(wen.ShutdownTimeout)
	if err != nil || stopWait < time.Second || stopWait > time.Minute {
		return Config{}, errors.New("MATCHCORE_SHUTDOWN_TIMEOUT 必须为 1s 到 1m")
	}
	token := get("MATCHCORE_API_TOKEN", "")
	if len(token) < 32 || len(token) > 256 {
		return Config{}, errors.New("MATCHCORE_API_TOKEN 必须为 32 到 256 字节的可见 ASCII 字符")
	}
	for _, ch := range token {
		if ch < 33 || ch > 126 {
			return Config{}, errors.New("MATCHCORE_API_TOKEN 必须为 32 到 256 字节的可见 ASCII 字符")
		}
	}
	return Config{
		HTTPAddr:        wen.HTTPAddr,
		APIToken:        token,
		RedisAddr:       wen.RedisAddr,
		RedisUsername:   wen.RedisUsername,
		RedisPassword:   get("MATCHCORE_REDIS_PASSWORD", ""),
		RedisDB:         wen.RedisDB,
		RedisTLS:        wen.RedisTLS,
		RedisPoolSize:   wen.RedisPoolSize,
		RedisTimeout:    redisWait,
		ShutdownTimeout: stopWait,
	}, nil
}

func validAddr(addr string, emptyHost bool) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || (!emptyHost && host == "") || strings.ContainsAny(host, " \t\r\n/@") {
		return false
	}
	num, err := strconv.Atoi(port)
	return err == nil && num >= 1 && num <= 65535
}
