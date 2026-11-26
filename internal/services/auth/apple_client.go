package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"root-backend-service/internal/core/domain"
)

const (
	appleIssuer      = "https://appleid.apple.com"
	appleKeysURL     = "https://appleid.apple.com/auth/keys"
	appleTokenURL    = "https://appleid.apple.com/auth/token"
	appleRevokeURL   = "https://appleid.apple.com/auth/revoke"
	appleKeysMaxAge  = time.Hour
	appleKeysMinWait = time.Minute
)

// AppleConfig son las credenciales de Sign in with Apple (variables APPLE_*).
type AppleConfig struct {
	TeamID string
	KeyID  string
	// PrivateKey es el contenido del archivo .p8 (PEM). Acepta "\n" escritos como texto.
	PrivateKey string
	// ClientIDs son los client_id aceptados: el bundle id de la app y, si hay login web, el Services ID.
	ClientIDs []string
}

// AppleClient implementa ports.AppleAuthClient.
type AppleClient struct {
	teamID, keyID string
	signingKey    *ecdsa.PrivateKey
	clientIDs     map[string]bool
	http          *http.Client
	keysURL       string
	tokenURL      string
	revokeURL     string
	now           func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewAppleClient devuelve nil, nil cuando Sign in with Apple no está configurado (sin client IDs).
// Sin la clave .p8 igual verifica logins, pero no puede canjear ni revocar tokens.
func NewAppleClient(cfg AppleConfig) (*AppleClient, error) {
	clientIDs := map[string]bool{}
	for _, id := range cfg.ClientIDs {
		if id = strings.TrimSpace(id); id != "" {
			clientIDs[id] = true
		}
	}
	if len(clientIDs) == 0 {
		return nil, nil
	}
	c := &AppleClient{
		teamID: strings.TrimSpace(cfg.TeamID), keyID: strings.TrimSpace(cfg.KeyID), clientIDs: clientIDs,
		http:    &http.Client{Timeout: 10 * time.Second},
		keysURL: appleKeysURL, tokenURL: appleTokenURL, revokeURL: appleRevokeURL, now: time.Now,
	}
	if strings.TrimSpace(cfg.PrivateKey) != "" {
		if c.teamID == "" || c.keyID == "" {
			return nil, errors.New("APPLE_TEAM_ID and APPLE_KEY_ID are required with APPLE_PRIVATE_KEY")
		}
		key, err := parseApplePrivateKey(cfg.PrivateKey)
		if err != nil {
			return nil, err
		}
		c.signingKey = key
	}
	return c, nil
}

// CanRevoke indica si hay clave para canjear y revocar tokens.
func (c *AppleClient) CanRevoke() bool { return c != nil && c.signingKey != nil }

func parseApplePrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("APPLE_PRIVATE_KEY must be the PEM content of the .p8 file")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("APPLE_PRIVATE_KEY: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("APPLE_PRIVATE_KEY must be an EC (ES256) key")
	}
	return key, nil
}

// appleIDClaims son los claims del identityToken. email_verified e is_private_email pueden venir como bool o como texto.
type appleIDClaims struct {
	jwt.RegisteredClaims
	Email          string    `json:"email"`
	EmailVerified  appleBool `json:"email_verified"`
	IsPrivateEmail appleBool `json:"is_private_email"`
	Nonce          string    `json:"nonce"`
}

type appleBool bool

func (b *appleBool) UnmarshalJSON(data []byte) error {
	value := strings.Trim(string(data), `"`)
	*b = appleBool(value == "true")
	return nil
}

func (c *AppleClient) Verify(ctx context.Context, identityToken, nonceHash string) (domain.AppleClaims, error) {
	var claims appleIDClaims
	_, err := jwt.ParseWithClaims(identityToken, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		return c.publicKey(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(appleIssuer), jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(c.now), jwt.WithLeeway(30*time.Second))
	if err != nil {
		return domain.AppleClaims{}, fmt.Errorf("%w: %v", domain.ErrInvalidAppleToken, err)
	}
	audience := ""
	for _, aud := range claims.Audience {
		if c.clientIDs[aud] {
			audience = aud
			break
		}
	}
	if audience == "" || claims.Subject == "" {
		return domain.AppleClaims{}, fmt.Errorf("%w: unexpected audience %v (accepted: APPLE_CLIENT_IDS)", domain.ErrInvalidAppleToken, []string(claims.Audience))
	}
	if nonceHash == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonceHash)) != 1 {
		return domain.AppleClaims{}, fmt.Errorf("%w: nonce mismatch (token has nonce: %t)", domain.ErrInvalidAppleToken, claims.Nonce != "")
	}
	return domain.AppleClaims{
		Subject: claims.Subject, Audience: audience, Email: strings.TrimSpace(claims.Email),
		EmailVerified: bool(claims.EmailVerified), PrivateEmail: bool(claims.IsPrivateEmail),
	}, nil
}

// publicKey devuelve la clave pública de Apple con ese kid. Las claves se cachean una hora y se vuelven a pedir
// si llega un kid desconocido (Apple las rota), como mucho una vez por minuto.
func (c *AppleClient) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key := c.keys[kid]; key != nil && c.now().Sub(c.fetchedAt) < appleKeysMaxAge {
		return key, nil
	}
	if c.keys != nil && c.now().Sub(c.fetchedAt) < appleKeysMinWait {
		if key := c.keys[kid]; key != nil {
			return key, nil
		}
		return nil, errors.New("unknown apple key id")
	}
	keys, err := c.fetchKeys(ctx)
	if err != nil {
		if key := c.keys[kid]; key != nil {
			return key, nil // Apple no respondió: se sigue con la clave cacheada.
		}
		return nil, err
	}
	c.keys, c.fetchedAt = keys, c.now()
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("unknown apple key id")
}

func (c *AppleClient) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.keysURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching apple keys: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching apple keys: HTTP %d", res.StatusCode)
	}
	var body struct {
		Keys []struct {
			Kid, Kty, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding apple keys: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range body.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return nil, errors.New("apple returned no usable keys")
	}
	return keys, nil
}

// clientSecret es el JWT ES256 que Apple pide para canjear y revocar tokens.
func (c *AppleClient) clientSecret(clientID string) (string, error) {
	if c.signingKey == nil {
		return "", domain.ErrAppleNotConfigured
	}
	now := c.now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer: c.teamID, Subject: clientID, Audience: jwt.ClaimStrings{appleIssuer},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	})
	token.Header["kid"] = c.keyID
	return token.SignedString(c.signingKey)
}

func (c *AppleClient) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apple request: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &problem)
		// Nunca se loguean tokens ni el client_secret.
		return nil, fmt.Errorf("apple HTTP %d (%s)", res.StatusCode, problem.Error)
	}
	return body, nil
}

func (c *AppleClient) Exchange(ctx context.Context, authorizationCode, clientID string) (string, error) {
	if !c.clientIDs[clientID] {
		return "", fmt.Errorf("unexpected apple client id")
	}
	secret, err := c.clientSecret(clientID)
	if err != nil {
		return "", err
	}
	body, err := c.postForm(ctx, c.tokenURL, url.Values{
		"client_id": {clientID}, "client_secret": {secret}, "code": {authorizationCode}, "grant_type": {"authorization_code"},
	})
	if err != nil {
		return "", fmt.Errorf("exchanging apple code: %w", err)
	}
	var token struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.RefreshToken == "" {
		return "", errors.New("apple did not return a refresh token")
	}
	return token.RefreshToken, nil
}

func (c *AppleClient) Revoke(ctx context.Context, refreshToken, clientID string) error {
	secret, err := c.clientSecret(clientID)
	if err != nil {
		return err
	}
	if _, err := c.postForm(ctx, c.revokeURL, url.Values{
		"client_id": {clientID}, "client_secret": {secret}, "token": {refreshToken}, "token_type_hint": {"refresh_token"},
	}); err != nil {
		return fmt.Errorf("revoking apple token: %w", err)
	}
	return nil
}
