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
		version = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *version {
		println("stilltemple-server " + versionString)
		return
	}

	if _, err := os.Stat(*webDir); err != nil {
		log.Printf("警告：静态目录不可用 %s: %v", *webDir, err)
	}

	rooms := NewRoomRegistry()
	presence := NewPresenceStore(90 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle("/api/rooms", roomsHandler(rooms, presence, *region))
	mux.Handle("/api/presence", presenceHandler(presence, rooms))
	mux.Handle("/", NewSite(*webDir))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           withHeaders(withLogging(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("stilltemple-server %s 启动，监听 %s，站点目录 %s", versionString, *addr, *webDir)
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
