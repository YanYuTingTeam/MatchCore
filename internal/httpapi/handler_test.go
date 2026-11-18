package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthAndReady(t *testing.T) {
	called := false
	handler := New(strings.Repeat("a", 32), 50*time.Millisecond, func(ctx context.Context) error {
		called = true
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("就绪检查未设置截止时间")
		}
		return errors.New("内部连接错误")
	})
	for _, item := range []struct {
		path string
		code int
		body string
	}{
		{"/healthz", http.StatusOK, "ok"},
		{"/readyz", http.StatusServiceUnavailable, "redis_unavailable"},
	} {
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, httptest.NewRequest(http.MethodGet, item.path, nil))
		if reply.Code != item.code || !strings.Contains(reply.Body.String(), item.body) {
			t.Fatalf("%s 响应异常: %d %s", item.path, reply.Code, reply.Body.String())
		}
		if item.path == "/healthz" && called {
			t.Fatal("存活检查访问了 Redis")
		}
		if strings.Contains(reply.Body.String(), "内部连接错误") {
			t.Fatal("响应泄露了内部错误")
		}
	}
	if !called {
		t.Fatal("就绪检查未访问 Redis")
	}
}

func TestReadySuccess(t *testing.T) {
	handler := New(strings.Repeat("a", 32), time.Second, func(context.Context) error { return nil })
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if reply.Code != http.StatusOK {
		t.Fatal("Redis 正常时未就绪")
	}
}

func TestAPIAuthAndPlaceholder(t *testing.T) {
	token := strings.Repeat("a", 32)
	handler := New(token, time.Second, func(context.Context) error { return nil })
	for _, item := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/groups"},
		{http.MethodGet, "/v1/groups/test/servers"},
		{http.MethodPost, "/v1/matches"},
	} {
		for _, allowed := range []bool{false, true} {
			request := httptest.NewRequest(item.method, item.path, nil)
			want := http.StatusUnauthorized
			if allowed {
				request.Header.Set("Authorization", "Bearer "+token)
				want = http.StatusServiceUnavailable
			}
			reply := httptest.NewRecorder()
			handler.ServeHTTP(reply, request)
			if reply.Code != want {
				t.Fatalf("%s 状态异常: %d", item.path, reply.Code)
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/groups", nil)
	request.Header.Add("Authorization", "Bearer "+token)
	request.Header.Add("Authorization", "Bearer "+token)
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, request)
	if reply.Code != http.StatusUnauthorized {
		t.Fatal("重复鉴权头未被拒绝")
	}
}
