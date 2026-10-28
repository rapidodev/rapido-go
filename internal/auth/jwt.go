package auth

import (
	"errors"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Access levels mirror the current Python contract's "access" claim exactly
// ("admin" vs "sudo") - see app/utils/jwt.py's create_admin_token.
const (
	AccessAdmin = "admin"
	AccessSudo  = "sudo"
)

var ErrInvalidToken = errors.New("auth: invalid or expired token")

type Claims struct {
	Username  string `json:"sub"`
	Access    string `json:"access"`
	Bootstrap bool   `json:"bootstrap,omitempty"`
	jwt.RegisteredClaims
}

func (c Claims) IsSudo() bool { return c.Access == AccessSudo }

type TokenIssuer struct {
	mu     sync.RWMutex
	secret []byte
	ttl    time.Duration // <= 0 means tokens never expire, matching JWT_ACCESS_TOKEN_EXPIRE_MINUTES <= 0
}

func NewTokenIssuer(secret []byte, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: secret, ttl: ttl}
}

func (i *TokenIssuer) Issue(username string, isSudo bool) (string, error) {
	return i.issue(username, isSudo, false)
}

func (i *TokenIssuer) IssueBootstrap(username string) (string, error) {
	return i.issue(username, true, true)
}

func (i *TokenIssuer) issue(username string, isSudo, bootstrap bool) (string, error) {
	access := AccessAdmin
	if isSudo {
		access = AccessSudo
	}
	claims := Claims{
		Username:  username,
		Bootstrap: bootstrap,
		Access:    access,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().UTC()),
		},
	}
	if i.ttl > 0 {
		claims.ExpiresAt = jwt.NewNumericDate(time.Now().UTC().Add(i.ttl))
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(i.Secret())
}

func (i *TokenIssuer) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return i.Secret(), nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Username == "" || (claims.Access != AccessAdmin && claims.Access != AccessSudo) {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (i *TokenIssuer) Secret() []byte {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]byte(nil), i.secret...)
}
func (i *TokenIssuer) UpdateSecret(secret []byte) {
	i.mu.Lock()
	i.secret = append([]byte(nil), secret...)
	i.mu.Unlock()
}
