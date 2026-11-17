package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRoomRegistry(t *testing.T) {
	r := NewRoomRegistry()
	if len(r.Snapshot(NewPresenceStore(time.Minute))) != RoomCount {
		t.Fatalf("期望 %d 间房", RoomCount)
	}
	for _, id := range []string{"maru", "maru-02", "maru-100"} {
		if !r.Has(id) {
			t.Fatalf("缺少房间 %s", id)
		}
	}
	if r.Has("maru-101") || r.Has("maru-01") {
		t.Fatal("不该存在 maru-101 或 maru-01")
	}
}

func TestPresenceMoveAndTTL(t *testing.T) {
	p := NewPresenceStore(50 * time.Millisecond)
	p.Heartbeat("u1", "maru-02")
	if got := p.Count("maru-02"); got != 1 {
		t.Fatalf("心跳后应为 1，实际 %d", got)
	}
	p.Heartbeat("u1", "maru-03")
	if got := p.Count("maru-02"); got != 0 {
		t.Fatalf("换房后旧房间应为 0，实际 %d", got)
	}
	if got := p.Count("maru-03"); got != 1 {
		t.Fatalf("新房应为 1，实际 %d", got)
	}
	time.Sleep(80 * time.Millisecond)
	if got := p.Count("maru-03"); got != 0 {
		t.Fatalf("超时后应为 0，实际 %d", got)
	}
}

func TestRoomsAPI(t *testing.T) {
	rooms := NewRoomRegistry()
	p := NewPresenceStore(time.Minute)
	h := roomsHandler(rooms, p, "apac")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rooms", nil))
	if rec.Code != 200 {
		t.Fatalf("状态码 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"maru-100"`) {
		t.Fatalf("响应缺少 maru-100: %s", rec.Body.String()[:120])
	}
}

func TestSiteLanguageRoutes(t *testing.T) {
	s := NewSite("../web")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/zh", nil))
	if rec.Code != 200 {
		t.Fatalf("/zh 应返回 200，实际 %d", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
		t.Fatal("/zh 未返回 HTML")
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../server/go.mod", nil))
	if rec.Code != 404 {
		t.Fatalf("越界访问应 404，实际 %d", rec.Code)
	}
}
