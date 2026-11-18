// Command stilltemple 是 Still Temple（静寺）的后端服务。
//
// 职责：
//   - 托管 web/ 目录下的静态站点（原站镜像，多语言路径）
//   - 提供房间目录接口 /api/rooms（原前端入口就是这个接口）
//   - 提供在场心跳接口 /api/presence，按房间统计真实在线人数
//   - 健康检查 /healthz
//
// 全部使用标准库，交叉编译后单文件部署，不依赖任何外部服务。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	var (
		addr    = flag.String("addr", envOr("STILLTEMPLE_ADDR", "127.0.0.1:8099"), "监听地址")
		webDir  = flag.String("web", envOr("STILLTEMPLE_WEB", "./web"), "静态站点目录")
		region  = flag.String("region", envOr("STILLTEMPLE_REGION", "apac"), "默认地区分片")
		apiOnly = flag.Bool("api-only", false, "只提供接口，不托管静态站点（静态由 nginx/CDN 输出）")
		version = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *version {
		println("stilltemple-server " + versionString)
		return
	}

	if !*apiOnly {
		if _, err := os.Stat(*webDir); err != nil {
			log.Printf("警告：静态目录不可用 %s: %v", *webDir, err)
		}
	}

	rooms := NewRoomRegistry()
	presence := NewPresenceStore(90 * time.Second)
	events := NewEventStore()
	live := NewLiveHub()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// 短地址：图文、视频里只印 stilltemple.app/still-temple
	mux.HandleFunc("/still-temple", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/xxjrq/still-temple", http.StatusFound)
	})
	mux.Handle("/api/rooms", roomsHandler(rooms, presence, *region, live))
	mux.Handle("/api/presence", presenceHandler(presence, rooms))
	mux.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { live.ServeHTTP(rooms, w, r) }))
	// 原前端把多人同步端点写死在 /presence（WebSocket）。这里复用同一处理器：
	// 普通 GET 返回真实在场人数，WS 升级请求回 426，避免整站出现大量 404。
	mux.Handle("/presence", presenceHandler(presence, rooms))
	mux.Handle("/api/event", eventHandler(events))
	mux.Handle("/api/stats", statsHandler(events))
	if *apiOnly {
		// 前后端分离：静态页面由 nginx 直接输出，本进程只负责接口。
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	} else {
		mux.Handle("/", NewSite(*webDir))
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           withHeaders(withCORS(withLogging(mux))),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("stilltemple-server %s 启动，监听 %s，静态目录 %s（api-only=%v）", versionString, *addr, *webDir, *apiOnly)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务退出: %v", err)
	}
}

const versionString = "0.1.0"

func logf(format string, args ...any) {
	log.Printf(format, args...)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
