package redisstore

import (
	"crypto/tls"
	"net"

	"matchcore/internal/config"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

func New(cfg config.Config) *redis.Client {
	options := &redis.Options{
		Addr:                  cfg.RedisAddr,
		Username:              cfg.RedisUsername,
		Password:              cfg.RedisPassword,
		DB:                    cfg.RedisDB,
		Protocol:              2,
		PoolSize:              cfg.RedisPoolSize,
		MaxActiveConns:        cfg.RedisPoolSize,
		PipelinePoolSize:      -1,
		MaxRetries:            -1,
		DialerRetries:         1,
		DialTimeout:           cfg.RedisTimeout,
		ReadTimeout:           cfg.RedisTimeout,
		WriteTimeout:          cfg.RedisTimeout,
		PoolTimeout:           cfg.RedisTimeout,
		ContextTimeoutEnabled: true,
		DisableIdentity:       true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		},
	}
	if cfg.RedisTLS {
		host, _, _ := net.SplitHostPort(cfg.RedisAddr)
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return redis.NewClient(options)
}
