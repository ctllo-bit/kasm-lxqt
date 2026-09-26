package auth

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"kclient/config"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/GehirnInc/crypt/sha256_crypt"
)

// Authenticator 持有认证信息。
type Authenticator struct {
	User string
	Hash string
}

// handleLogin 处理 POST /login：
//   - 校验用户名密码
//   - 创建 session
//   - 下发 kclient_session cookie
//   - 重定向到首页
//
// 失败时返回一段 alert + 跳回登录页的 HTML。
func LoginHandler(cfg config.Config, authenticator *Authenticator, sessionStore *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.FormValue("username")
		password := r.FormValue("password")

		// 验证 .kasmpasswd
		if !authenticator.Verify(username, password) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<script>alert("用户名或密码错误");location.href=%q;</script>`, cfg.ResolvePath("/login"))
			return
		}

		// 生成 Basic Auth
		raw := username + ":" + password
		authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))

		// 创建 session
		sessionID, err := sessionStore.Create(authorization)
		if err != nil {
			http.Error(w, "failed to create session", http.StatusInternalServerError)
			return
		}

		cookie := &http.Cookie{
			Name:     "kclient_session",
			Value:    sessionID,
			Path:     cfg.Subfolder,
			HttpOnly: true,
			Secure:   false, // 调试期强制 false
			SameSite: http.SameSiteLaxMode,
		}

		log.Printf("SetCookie: name=%s path=%s secure=%v r.TLS=%v", cookie.Name, cookie.Path, cookie.Secure, r.TLS != nil)

		http.SetCookie(w, cookie)
		// 登录成功，重定项主页
		http.Redirect(w, r, cfg.ResolvePath("/"), http.StatusSeeOther)
	}
}

// Load 从 KasmVNC 的 .kasmpasswd 文件加载认证信息。
func Load(path string) (*Authenticator, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return nil, errors.New("empty kasmpasswd file")
	}

	parts := strings.Split(scanner.Text(), ":")
	if len(parts) < 2 {
		return nil, errors.New("invalid kasmpasswd format")
	}

	return &Authenticator{
		User: parts[0],
		Hash: parts[1],
	}, nil
}

// Verify 校验用户名和密码。
func (a *Authenticator) Verify(user, pass string) bool {
	if user != a.User {
		return false
	}
	// KasmVNC 使用 SHA-256 crypt，即 $5$ 前缀。
	c := sha256_crypt.New()
	return c.Verify(a.Hash, []byte(pass)) == nil
}

// Session Middleware 用 KasmVNC 的密码哈希校验，通过后 Authorization 透传给代理
func (s *SessionStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// 登录页面和登录接口不需要认证
		if r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}

		_, ok := s.GetFromRequest(r)

		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}
