package main

// 匿名埋点：只统计"有没有人来、待了多久、切了几次房间"，
// 不记录 IP、不写 cookie、不落库，重启即清零。

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
)

type eventPayload struct {
	Event string `json:"e"`    // enter / hide / leave / room / lang
	Room  int    `json:"room"` // 1-100
	Lang  string `json:"lang"` // zh / en / ...
	Dur   int    `json:"d"`    // 秒，仅 leave/hide
}

type EventStore struct {
	mu      sync.Mutex
	enters  int
	leaves  int
	dwellNs int64 // 累计秒
	dwellN  int64
	rooms   map[int]int
	langs   map[string]int
}

func NewEventStore() *EventStore {
	return &EventStore{rooms: map[int]int{}, langs: map[string]int{}}
}

func (s *EventStore) add(p eventPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch p.Event {
	case "enter":
		s.enters++
		// 前端把语言和房间号都挂在 enter 事件上发过来，这里一并记下，
		// 否则 langs / top_rooms 永远是空。
		if p.Lang != "" && len(p.Lang) <= 8 {
			s.langs[p.Lang]++
		}
		if p.Room >= 1 && p.Room <= 100 {
			s.rooms[p.Room]++
		}
	case "leave", "hide":
		if p.Dur > 0 && p.Dur < 60*60*6 { // 超过 6 小时视为挂机，不计入
			s.dwellNs += int64(p.Dur)
			s.dwellN++
			s.leaves++
		}
	case "room":
		if p.Room >= 1 && p.Room <= 100 {
			s.rooms[p.Room]++
		}
	case "lang":
		if p.Lang != "" && len(p.Lang) <= 8 {
			s.langs[p.Lang]++
		}
	}
}

func (s *EventStore) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	avg := 0.0
	if s.dwellN > 0 {
		avg = float64(s.dwellNs) / float64(s.dwellN)
	}
	topRooms := make([]kv, 0, len(s.rooms))
	for k, v := range s.rooms {
		topRooms = append(topRooms, kv{k, v})
	}
	sort.Slice(topRooms, func(i, j int) bool { return topRooms[i].N > topRooms[j].N })

	return map[string]any{
		"ok":            true,
		"enters":        s.enters,
		"sessions":      s.dwellN,
		"avg_seconds":   int(avg),
		"langs":         s.langs,
		"top_rooms":     topRooms,
		"note":          "匿名统计，不存 IP，不落库",
	}
}

type kv struct {
	Room int `json:"room"`
	N    int `json:"n"`
}

func eventHandler(s *EventStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var p eventPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.add(p)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func statsHandler(s *EventStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(s.snapshot())
	})
}
