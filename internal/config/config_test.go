package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("MATCHCORE_API_TOKEN", strings.Repeat("a", 32))
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("MATCHCORE_REDIS_ADDR=[::1]:6379\nMATCHCORE_REDIS_TIMEOUT=500ms\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.RedisAddr != "[::1]:6379" || cfg.RedisTimeout != 500*time.Millisecond {
		t.Fatal("配置默认值或文件值异常")
	}
}

func TestLoadFileValues(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), ".env")
	data := "export MATCHCORE_HTTP_ADDR='127.0.0.1:9090' # 地址\n" +
		"MATCHCORE_API_TOKEN='" + strings.Repeat("a", 32) + "'\n" +
		"MATCHCORE_REDIS_ADDR=[::1]:6380\nMATCHCORE_REDIS_USERNAME=\"redis_user\"\n" +
		"MATCHCORE_REDIS_PASSWORD='pass=word#dollar$quote\"end'\n" +
		"MATCHCORE_REDIS_DB=2\nMATCHCORE_REDIS_TLS=true\nMATCHCORE_REDIS_POOL_SIZE=12\n" +
		"MATCHCORE_REDIS_TIMEOUT=500ms\nMATCHCORE_SHUTDOWN_TIMEOUT=20s\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9090" || cfg.APIToken != strings.Repeat("a", 32) ||
		cfg.RedisAddr != "[::1]:6380" || cfg.RedisUsername != "redis_user" ||
		cfg.RedisPassword != "pass=word#dollar$quote\"end" || cfg.RedisDB != 2 || !cfg.RedisTLS ||
		cfg.RedisPoolSize != 12 || cfg.RedisTimeout != 500*time.Millisecond || cfg.ShutdownTimeout != 20*time.Second {
		t.Fatal("完整 dotenv 配置解析异常")
	}
	if _, ok := os.LookupEnv("MATCHCORE_API_TOKEN"); ok {
		t.Fatal("文件配置污染了进程环境")
	}
}

func TestLoadEnvOverride(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), ".env")
	data := "MATCHCORE_HTTP_ADDR=127.0.0.1:9090\nMATCHCORE_API_TOKEN=" + strings.Repeat("a", 32) + "\n" +
		"MATCHCORE_REDIS_ADDR=127.0.0.1:6380\nMATCHCORE_REDIS_USERNAME=file_user\n" +
		"MATCHCORE_REDIS_PASSWORD=file_password\nMATCHCORE_REDIS_DB=2\nMATCHCORE_REDIS_TLS=true\n" +
		"MATCHCORE_REDIS_POOL_SIZE=12\nMATCHCORE_REDIS_TIMEOUT=500ms\nMATCHCORE_SHUTDOWN_TIMEOUT=20s\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"MATCHCORE_HTTP_ADDR": "127.0.0.1:8081", "MATCHCORE_API_TOKEN": strings.Repeat("b", 32),
		"MATCHCORE_REDIS_ADDR": "[::1]:6379", "MATCHCORE_REDIS_USERNAME": "env_user",
		"MATCHCORE_REDIS_PASSWORD": "", "MATCHCORE_REDIS_DB": "3", "MATCHCORE_REDIS_TLS": "false",
		"MATCHCORE_REDIS_POOL_SIZE": "15", "MATCHCORE_REDIS_TIMEOUT": "1s", "MATCHCORE_SHUTDOWN_TIMEOUT": "30s",
	} {
		t.Setenv(key, value)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8081" || cfg.APIToken != strings.Repeat("b", 32) ||
		cfg.RedisAddr != "[::1]:6379" || cfg.RedisUsername != "env_user" || cfg.RedisPassword != "" ||
		cfg.RedisDB != 3 || cfg.RedisTLS || cfg.RedisPoolSize != 15 || cfg.RedisTimeout != time.Second ||
		cfg.ShutdownTimeout != 30*time.Second {
		t.Fatal("进程环境未覆盖文件配置")
	}
	t.Setenv("MATCHCORE_API_TOKEN", "")
	if _, err := Load(path); err == nil {
		t.Fatal("显式空进程令牌未覆盖文件令牌")
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	for _, data := range []string{
		`null`, `[]`, `{} {}`, `MATCHCORE_UNKNOWN=1`, `MATCHCORE_HTTP_ADDR=127.0.0.1:0`,
		`MATCHCORE_REDIS_ADDR=:6379`, `MATCHCORE_REDIS_TIMEOUT=0s`, `MATCHCORE_REDIS_POOL_SIZE=0`,
		`MATCHCORE_REDIS_DB=-1`, `MATCHCORE_REDIS_DB=wrong`, `MATCHCORE_REDIS_TLS=wrong`,
		`MATCHCORE_REDIS_POOL_SIZE=129`, `MATCHCORE_REDIS_TIMEOUT=6s`, `MATCHCORE_SHUTDOWN_TIMEOUT=0s`,
		`MATCHCORE_REDIS_PASSWORD='private_value`,
	} {
		t.Run(data, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("MATCHCORE_API_TOKEN", strings.Repeat("a", 32))
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("错误配置未被拒绝")
			} else if strings.Contains(err.Error(), "private_value") {
				t.Fatal("配置错误包含密钥内容")
			}
		})
	}
}

func TestLoadRejectsBadToken(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", strings.Repeat("a", 31), strings.Repeat("a", 257), strings.Repeat("a", 32) + "\v", strings.Repeat("a", 32) + " ", strings.Repeat("a", 32) + "中"} {
		t.Setenv("MATCHCORE_API_TOKEN", token)
		if _, err := Load(path); err == nil {
			t.Fatal("错误令牌未被拒绝")
		}
	}
}

func TestLoadFileBoundary(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("MATCHCORE_API_TOKEN", strings.Repeat("a", 32))
	path := filepath.Join(t.TempDir(), ".env")
	if _, err := Load(path); err == nil {
		t.Fatal("缺失配置文件未被拒绝")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", 64*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("超限配置文件未被拒绝")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MATCHCORE_HTTP_ADDR", "MATCHCORE_API_TOKEN", "MATCHCORE_REDIS_ADDR", "MATCHCORE_REDIS_USERNAME",
		"MATCHCORE_REDIS_PASSWORD", "MATCHCORE_REDIS_DB", "MATCHCORE_REDIS_TLS", "MATCHCORE_REDIS_POOL_SIZE",
		"MATCHCORE_REDIS_TIMEOUT", "MATCHCORE_SHUTDOWN_TIMEOUT",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
