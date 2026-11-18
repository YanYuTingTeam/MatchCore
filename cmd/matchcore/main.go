package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"matchcore/internal/config"
	"matchcore/internal/groups"
	"matchcore/internal/httpapi"
	"matchcore/internal/matching"
	"matchcore/internal/redisstore"
	"matchcore/internal/serverquery"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", ".env", "配置文件路径")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	groupPath := cfg.GroupsFile
	if !filepath.IsAbs(groupPath) {
		groupPath = filepath.Join(filepath.Dir(*path), groupPath)
	}
	rules, err := groups.Load(groupPath)
	if err != nil {
		return err
	}
	if !rules.ValidNamespace(cfg.RedisPrefix) {
		return errors.New("Redis快照前缀不能与原始房间前缀重叠")
	}
	rdb := redisstore.New(cfg)
	defer rdb.Close()
	service := matching.New(cfg, rules, rdb, serverquery.TCPClient{Timeout: cfg.RedisTimeout})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	collectorEnd := make(chan struct{})
	go func() {
		defer close(collectorEnd)
		for {
			if err := service.Refresh(ctx); err != nil && ctx.Err() == nil {
				log.Print("房间采集或快照写入失败, 本次不更新状态")
			}
			timer := time.NewTimer(cfg.RefreshInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	defer func() { stop(); <-collectorEnd }()
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.New(cfg.APIToken, cfg.RedisTimeout, func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		}, service),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	end := make(chan error, 1)
	go func() {
		end <- server.ListenAndServe()
	}()
	log.Printf("MatchCore 正在启动, 监听 %s", cfg.HTTPAddr)
	select {
	case err := <-end:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务启动失败: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	stop()
	closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(closeCtx); err != nil {
		_ = server.Close()
		return errors.New("HTTP 服务未在关闭时限内退出")
	}
	log.Print("MatchCore 已关闭")
	return nil
}
