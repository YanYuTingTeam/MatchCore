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
	"syscall"
	"time"

	"matchcore/internal/config"
	"matchcore/internal/httpapi"
	"matchcore/internal/redisstore"
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
	rdb := redisstore.New(cfg)
	defer rdb.Close()
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.New(cfg.APIToken, cfg.RedisTimeout, func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
