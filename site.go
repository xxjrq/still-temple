package main

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// langRoutes 把原站的语言路径映射到对应的页面文件。
var langRoutes = map[string]string{
	"/":      "index.html",
	"/ko":    "index.html",
	"/zh":    "zh.html",
	"/ja":    "ja.html",
	"/en":    "en.html",
	"/es":    "es.html",
	"/fr":    "fr.html",
	"/zh-cn": "zh.html",
	"/zh-hant": "zh-Hant.html",
}

// Site 托管静态站点：语言路由、缓存头、预压缩文件。
type Site struct {
	root string
}

func NewSite(root string) *Site { return &Site{root: root} }

func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	// 语言路径大小写不敏感：/zh-Hant 与 /zh-hant 都能命中
	if file, ok := langRoutes[strings.ToLower(clean)]; ok {
		s.sendFile(w, r, file, true)
		return
	}

	// /about 以及各语言前缀下的 /xx/about 都指向同一份说明页
	if clean == "/about" || strings.HasSuffix(clean, "/about") {
		s.sendFile(w, r, "about.html", true)
		return
	}

	// 目录一律回落到首页，避免越界和目录列表
	if strings.HasSuffix(r.URL.Path, "/") {
		s.sendFile(w, r, "index.html", true)
		return
	}

	rel := strings.TrimPrefix(clean, "/")
	if rel == "" {
		s.sendFile(w, r, "index.html", true)
		return
	}
	s.sendFile(w, r, rel, false)
}

func (s *Site) sendFile(w http.ResponseWriter, r *http.Request, rel string, html bool) {
	// 只允许访问 root 内部文件
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, filepath.Clean(s.root)+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		if html {
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}

	ctype := mime.TypeByExtension(filepath.Ext(full))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	if ext := filepath.Ext(full); ext == ".html" {
		ctype = "text/html; charset=utf-8"
	} else if ext == ".js" {
		ctype = "text/javascript; charset=utf-8"
	} else if ext == ".css" {
		ctype = "text/css; charset=utf-8"
	} else if ext == ".webmanifest" {
		ctype = "application/manifest+json; charset=utf-8"
	}

	w.Header().Set("Content-Type", ctype)
	if html {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Vary", "Accept-Encoding")

	// 预压缩：优先发同名 .gz，跳过 Range
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && r.Header.Get("Range") == "" {
		if gz, err := os.Stat(full + ".gz"); err == nil && !gz.IsDir() {
			w.Header().Set("Content-Encoding", "gzip")
			http.ServeFile(w, r, full+".gz")
			return
		}
	}

	http.ServeFile(w, r, full)
}

// withHeaders 加一组与业务无关的通用安全头。
func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// withCORS 只放行 Capacitor app 的本地源访问 /api/*，网页同源请求不受影响。
func withCORS(next http.Handler) http.Handler {
	allowed := map[string]bool{
		"capacitor://localhost": true,
		"https://localhost":     true,
		"http://localhost":      true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] && strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withLogging 只打印接口与错误，静态资源不刷日志。
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket handlers must retain the underlying Hijacker implementation.
		if r.URL.Path == "/ws" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") || rec.status >= 400 {
			logf("%s %s %d %s %s", r.Method, r.URL.RequestURI(), rec.status, clientIP(r), time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		if i := strings.Index(ip, ","); i > 0 {
			return strings.TrimSpace(ip[:i])
		}
		return strings.TrimSpace(ip)
	}
	return r.RemoteAddr
}
