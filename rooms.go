package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RoomCapacity 是原站的分片容量：每间房最多 100 人。
const RoomCapacity = 100

// RoomCount 是分片总数：原站写死 100 间。
const RoomCount = 100

// Room 是 /api/rooms 返回的单条记录，字段对齐原站前端消费者的读取习惯。
type Room struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Capacity  int    `json:"capacity"`
	Count     int    `json:"count"`
	LiveCount int    `json:"liveCount"`
}

// RoomRegistry 生成固定的 100 间分片，id 规则为 maru、maru-02 … maru-100。
type RoomRegistry struct {
	rooms []Room
	byID  map[string]int
}

func NewRoomRegistry() *RoomRegistry {
	r := &RoomRegistry{
		rooms: make([]Room, 0, RoomCount),
		byID:  make(map[string]int, RoomCount),
	}
	for i := 1; i <= RoomCount; i++ {
		id := "maru"
		if i > 1 {
			id = fmt.Sprintf("maru-%02d", i)
		}
		r.byID[id] = len(r.rooms)
		r.rooms = append(r.rooms, Room{ID: id, Number: i, Capacity: RoomCapacity})
	}
	return r
}

func (r *RoomRegistry) Has(id string) bool {
	_, ok := r.byID[id]
	return ok
}

func (r *RoomRegistry) NumberOf(id string) int {
	if idx, ok := r.byID[id]; ok {
		return r.rooms[idx].Number
	}
	return 0
}

// Snapshot 返回带人数的房间列表。人数 = 真实心跳在线 + 氛围人数（封顶容量）。
func (r *RoomRegistry) Snapshot(p *PresenceStore, hubs ...*LiveHub) []Room {
	out := make([]Room, len(r.rooms))
	copy(out, r.rooms)
	for i := range out {
		live := p.Count(out[i].ID)
		ambient := ambientResidents(out[i].Number)
		total := live + ambient
		if total > out[i].Capacity {
			total = out[i].Capacity
		}
		out[i].Count = total
		if len(hubs) > 0 && hubs[0] != nil {
			out[i].LiveCount = hubs[0].Count(out[i].ID)
		}
	}
	return out
}

// ambientResidents 是"氛围人数"：让每间房不至于空得可怕，数值只由房号决定，
// 重启也不会跳变（所以看起来像是有人一直在）。
// 注意：这是模拟值，不是真实在线人数，对外的日活/访问统计一律不采用它，只用 /api/stats 的真实埋点。
func ambientResidents(number int) int {
	if number == 1 {
		return 6 // 默认入口房，给多一点
	}
	return 2 + (number*7)%5 // 2–6 人
}

func roomsHandler(rooms *RoomRegistry, p *PresenceStore, region string, live ...*LiveHub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			OK     bool   `json:"ok"`
			Region string `json:"region"`
			Rooms  []Room `json:"rooms"`
		}{true, region, rooms.Snapshot(p, live...)})
	})
}

func presenceHandler(p *PresenceStore, rooms *RoomRegistry) http.Handler {
	type request struct {
		ID   string `json:"id"`
		Room string `json:"room"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")

		// 前端仍会尝试用 WebSocket 连这个路径做多人同步；我们暂未实现实时服务，
		// 明确回 426 而不是 404，避免把"功能未实现"伪装成"地址不存在"。
		if strings.Contains(r.Header.Get("Upgrade"), "websocket") {
			w.Header().Set("Connection", "Upgrade")
			w.Header().Set("Upgrade", "websocket")
			http.Error(w, `{"ok":false,"error":"realtime sync not implemented"}`, http.StatusUpgradeRequired)
			return
		}

		switch r.Method {
		case http.MethodGet, http.MethodHead:
			room := r.URL.Query().Get("room")
			if room == "" || !rooms.Has(room) {
				room = "maru"
			}
			_ = json.NewEncoder(w).Encode(struct {
				OK    bool   `json:"ok"`
				Room  string `json:"room"`
				Count int    `json:"count"`
			}{true, room, minInt(p.Count(room)+ambientResidents(rooms.NumberOf(room)), RoomCapacity)})
		case http.MethodPost, http.MethodPut:
			var req request
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
				http.Error(w, `{"ok":false,"error":"bad json"}`, http.StatusBadRequest)
				return
			}
			if req.ID == "" || len(req.ID) > 64 {
				http.Error(w, `{"ok":false,"error":"bad id"}`, http.StatusBadRequest)
				return
			}
			if !rooms.Has(req.Room) {
				http.Error(w, `{"ok":false,"error":"unknown room"}`, http.StatusBadRequest)
				return
			}
			p.Heartbeat(req.ID, req.Room)
			_ = json.NewEncoder(w).Encode(struct {
				OK    bool `json:"ok"`
				Count int  `json:"count"`
			}{true, minInt(p.Count(req.Room)+ambientResidents(rooms.NumberOf(req.Room)), RoomCapacity)})

		case http.MethodDelete:
			var req request
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
			if req.ID != "" {
				p.Leave(req.ID)
			}
			_ = json.NewEncoder(w).Encode(struct {
				OK bool `json:"ok"`
			}{true})

		default:
			w.Header().Set("Allow", "POST, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// PresenceStore 是内存里的在场表：visitorID -> 房间 + 最后心跳时间。
// 超过 TTL 没有心跳就自动过期，进程重启即清空，不落盘。
type PresenceStore struct {
	mu      sync.RWMutex
	rooms   map[string]map[string]time.Time
	ttl     time.Duration
	stopJan chan struct{}
}

func NewPresenceStore(ttl time.Duration) *PresenceStore {
	p := &PresenceStore{
		rooms:   make(map[string]map[string]time.Time),
		ttl:     ttl,
		stopJan: make(chan struct{}),
	}
	go p.janitor(30 * time.Second)
	return p
}

func (p *PresenceStore) Heartbeat(id, room string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// 换房时先从旧房间移除，避免同一个人被统计两次
	for rid, set := range p.rooms {
		if rid != room {
			delete(set, id)
		}
	}
	set, ok := p.rooms[room]
	if !ok {
		set = make(map[string]time.Time)
		p.rooms[room] = set
	}
	set[id] = time.Now()
}

func (p *PresenceStore) Leave(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, set := range p.rooms {
		delete(set, id)
	}
}

func (p *PresenceStore) Count(room string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	n := 0
	for _, seen := range p.rooms[room] {
		if now.Sub(seen) <= p.ttl {
			n++
		}
	}
	return n
}

func (p *PresenceStore) janitor(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			p.sweep()
		case <-p.stopJan:
			return
		}
	}
}

func (p *PresenceStore) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for room, set := range p.rooms {
		for id, seen := range set {
			if now.Sub(seen) > p.ttl {
				delete(set, id)
			}
		}
		if len(set) == 0 {
			delete(p.rooms, room)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
