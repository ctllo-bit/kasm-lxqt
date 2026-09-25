package auth

import (
	"bufio"
	"encoding/base64"
	"errors"
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

func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if !a.Verify(username, password) {
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}

	// 登录成功
	// 后面创建 session
}

func LoginHandler(a *Authenticator, store *SessionStore) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		username := r.FormValue("username")
		password := r.FormValue("password")

		// 验证 .kasmpasswd
		if !a.Verify(username, password) {
			http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
			return
		}

		// 生成 Basic Auth
		raw := username + ":" + password

		authorization := "Basic " +
			base64.StdEncoding.EncodeToString([]byte(raw))

		// 创建 session
		sessionID, err := store.Create(authorization)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "kclient_session",
			Value:    sessionID,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})

		// 登录成功
		http.Redirect(w, r, "/", http.StatusSeeOther)
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
