package auth

import (
	"bufio"
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

// basicAuth 用 KasmVNC 的密码哈希校验，通过后 Authorization 透传给代理
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !a.Verify(user, pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="kclient"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
