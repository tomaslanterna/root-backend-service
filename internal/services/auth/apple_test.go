package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

const testBundleID = "com.emnetwork.root"

type appleTestServer struct {
	server    *httptest.Server
	key       *rsa.PrivateKey
	forms     []map[string]string
	ecdsaPub  *ecdsa.PublicKey
	tokenFail bool
}

func newAppleTestServer(t *testing.T) *appleTestServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &appleTestServer{key: key}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kid": "test-kid", "kty": "RSA", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
		case "/token", "/revoke":
			r.ParseForm()
			form := map[string]string{"path": r.URL.Path}
			for k := range r.PostForm {
				form[k] = r.PostForm.Get(k)
			}
			s.forms = append(s.forms, form)
			// El client_secret tiene que estar firmado con la clave .p8 (ES256).
			if _, err := jwt.Parse(form["client_secret"], func(*jwt.Token) (any, error) { return s.ecdsaPub, nil },
				jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer("TEAM123"), jwt.WithSubject(form["client_id"])); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_client"}`))
				return
			}
			if s.tokenFail {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			if r.URL.Path == "/token" {
				w.Write([]byte(`{"refresh_token":"refresh-1","id_token":"x"}`))
			}
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *appleTestServer) idToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	base := jwt.MapClaims{
		"iss": appleIssuer, "aud": testBundleID, "sub": "apple-sub-1",
		"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
		"email": "ana@example.com", "email_verified": "true", "nonce": HashAppleNonce("raw-nonce"),
	}
	for k, v := range claims {
		base[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, base)
	token.Header["kid"] = "test-kid"
	signed, err := token.SignedString(s.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func (s *appleTestServer) client(t *testing.T) *AppleClient {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s.ecdsaPub = &ecKey.PublicKey
	der, _ := x509.MarshalPKCS8PrivateKey(ecKey)
	pemKey := strings.ReplaceAll(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "\n", `\n`)
	c, err := NewAppleClient(AppleConfig{TeamID: "TEAM123", KeyID: "KEY123", PrivateKey: pemKey, ClientIDs: []string{testBundleID, " com.emnetwork.root.web "}})
	if err != nil || c == nil {
		t.Fatalf("client: %v", err)
	}
	c.keysURL, c.tokenURL, c.revokeURL = s.server.URL+"/keys", s.server.URL+"/token", s.server.URL+"/revoke"
	return c
}

func TestAppleClientVerify(t *testing.T) {
	s := newAppleTestServer(t)
	c := s.client(t)
	ctx := context.Background()

	claims, err := c.Verify(ctx, s.idToken(t, jwt.MapClaims{"is_private_email": true}), HashAppleNonce("raw-nonce"))
	if err != nil || claims.Subject != "apple-sub-1" || claims.Audience != testBundleID || !claims.EmailVerified || !claims.PrivateEmail {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	for name, token := range map[string]string{
		"other app":    s.idToken(t, jwt.MapClaims{"aud": "com.other.app"}),
		"other issuer": s.idToken(t, jwt.MapClaims{"iss": "https://evil.example"}),
		"expired":      s.idToken(t, jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()}),
		"other nonce":  s.idToken(t, jwt.MapClaims{"nonce": HashAppleNonce("other")}),
		"garbage":      "not-a-jwt",
	} {
		if _, err := c.Verify(ctx, token, HashAppleNonce("raw-nonce")); !errors.Is(err, domain.ErrInvalidAppleToken) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	// Firmado con otra clave.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": appleIssuer, "aud": testBundleID, "sub": "x",
		"exp": time.Now().Add(time.Minute).Unix(), "nonce": HashAppleNonce("raw-nonce")})
	forged.Header["kid"] = "test-kid"
	forgedToken, _ := forged.SignedString(other)
	if _, err := c.Verify(ctx, forgedToken, HashAppleNonce("raw-nonce")); !errors.Is(err, domain.ErrInvalidAppleToken) {
		t.Fatalf("forged token accepted: %v", err)
	}
}

func TestAppleClientExchangeAndRevoke(t *testing.T) {
	s := newAppleTestServer(t)
	c := s.client(t)
	token, err := c.Exchange(context.Background(), "code-1", testBundleID)
	if err != nil || token != "refresh-1" {
		t.Fatalf("exchange: %q %v", token, err)
	}
	if err := c.Revoke(context.Background(), "refresh-1", testBundleID); err != nil {
		t.Fatal(err)
	}
	if len(s.forms) != 2 || s.forms[0]["code"] != "code-1" || s.forms[0]["grant_type"] != "authorization_code" ||
		s.forms[1]["token"] != "refresh-1" || s.forms[1]["token_type_hint"] != "refresh_token" {
		t.Fatalf("forms %v", s.forms)
	}
	if _, err := c.Exchange(context.Background(), "code-1", "com.other.app"); err == nil {
		t.Fatal("exchange for an unknown client id")
	}
	s.tokenFail = true
	if _, err := c.Exchange(context.Background(), "code-1", testBundleID); err == nil || strings.Contains(err.Error(), "refresh") {
		t.Fatalf("error must not leak tokens: %v", err)
	}
}

func TestNewAppleClientIsOptional(t *testing.T) {
	if c, err := NewAppleClient(AppleConfig{ClientIDs: []string{"", " "}}); c != nil || err != nil {
		t.Fatalf("without client ids: %v %v", c, err)
	}
	c, err := NewAppleClient(AppleConfig{ClientIDs: []string{testBundleID}})
	if err != nil || c == nil || c.CanRevoke() {
		t.Fatalf("without key it verifies but cannot revoke: %v %v", c, err)
	}
	if _, err := NewAppleClient(AppleConfig{ClientIDs: []string{testBundleID}, PrivateKey: "nope", TeamID: "T", KeyID: "K"}); err == nil {
		t.Fatal("invalid key accepted")
	}
}

// ---------- AppleLogin ----------

type appleUsersFake struct {
	ports.UserRepository
	byEmail map[string]*domain.User
	byID    map[string]*domain.User
	created []*domain.User
}

func (f *appleUsersFake) GetUserByEmail(_ context.Context, email string) (*domain.User, error) {
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}
func (f *appleUsersFake) GetUserByID(_ context.Context, id string) (*domain.User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, errors.New("user not found")
}
func (f *appleUsersFake) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	for _, u := range f.byID {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, domain.ErrUserNotFound
}
func (f *appleUsersFake) CreateUser(_ context.Context, u *domain.User) error {
	f.created = append(f.created, u)
	f.byID[u.ID] = u
	return nil
}

type appleIdentitiesFake struct {
	subjects map[string]string
	refresh  map[string]string
	links    []string
}

func (f *appleIdentitiesFake) UserIDBySubject(_ context.Context, sub string) (string, error) {
	if id, ok := f.subjects[sub]; ok {
		return id, nil
	}
	return "", domain.ErrUserNotFound
}
func (f *appleIdentitiesFake) Link(_ context.Context, userID, sub, _, _ string) error {
	f.subjects[sub] = userID
	f.links = append(f.links, userID)
	return nil
}
func (f *appleIdentitiesFake) SetRefreshToken(_ context.Context, userID, _, token string) error {
	f.refresh[userID] = token
	return nil
}
func (f *appleIdentitiesFake) RefreshToken(_ context.Context, userID string) (string, string, error) {
	return f.refresh[userID], testBundleID, nil
}

type appleClientFake struct {
	claims  domain.AppleClaims
	revoked []string
}

func (f *appleClientFake) Verify(_ context.Context, _, nonceHash string) (domain.AppleClaims, error) {
	if nonceHash != HashAppleNonce("raw-nonce") {
		return domain.AppleClaims{}, domain.ErrInvalidAppleToken
	}
	return f.claims, nil
}
func (f *appleClientFake) Exchange(context.Context, string, string) (string, error) {
	return "refresh-new", nil
}
func (f *appleClientFake) Revoke(_ context.Context, token, _ string) error {
	f.revoked = append(f.revoked, token)
	return nil
}

func appleService(users *appleUsersFake, ids *appleIdentitiesFake, claims domain.AppleClaims) (*authService, *appleClientFake) {
	client := &appleClientFake{claims: claims}
	svc := NewAuthService(users)
	svc.UseApple(client, ids)
	return svc, client
}

func TestAppleLoginFlows(t *testing.T) {
	t.Setenv("JWT_SECRET", "auth-test-secret")
	input := domain.AppleLoginInput{IdentityToken: "token", AuthorizationCode: "code", Nonce: "raw-nonce", GivenName: "Ana", FamilyName: "Paz"}
	existing := &domain.User{ID: "existing", Email: "ana@example.com", Username: "ana", Role: "USER"}

	t.Run("linked account", func(t *testing.T) {
		users := &appleUsersFake{byEmail: map[string]*domain.User{}, byID: map[string]*domain.User{"existing": existing}}
		ids := &appleIdentitiesFake{subjects: map[string]string{"sub": "existing"}, refresh: map[string]string{}}
		svc, _ := appleService(users, ids, domain.AppleClaims{Subject: "sub", Audience: testBundleID})
		token, user, err := svc.AppleLogin(context.Background(), input)
		if err != nil || token == "" || user.ID != "existing" || len(users.created) != 0 || ids.refresh["existing"] != "refresh-new" {
			t.Fatalf("token=%q user=%+v err=%v created=%d refresh=%v", token, user, err, len(users.created), ids.refresh)
		}
	})

	t.Run("links by verified email", func(t *testing.T) {
		users := &appleUsersFake{byEmail: map[string]*domain.User{"ana@example.com": existing}, byID: map[string]*domain.User{"existing": existing}}
		ids := &appleIdentitiesFake{subjects: map[string]string{}, refresh: map[string]string{}}
		svc, _ := appleService(users, ids, domain.AppleClaims{Subject: "sub", Audience: testBundleID, Email: "Ana@Example.com", EmailVerified: true})
		_, user, err := svc.AppleLogin(context.Background(), input)
		if err != nil || user.ID != "existing" || ids.subjects["sub"] != "existing" || len(users.created) != 0 {
			t.Fatalf("user=%+v err=%v", user, err)
		}
	})

	t.Run("private email creates a new account", func(t *testing.T) {
		users := &appleUsersFake{byEmail: map[string]*domain.User{"ana@example.com": existing}, byID: map[string]*domain.User{"existing": existing}}
		ids := &appleIdentitiesFake{subjects: map[string]string{}, refresh: map[string]string{}}
		svc, _ := appleService(users, ids, domain.AppleClaims{Subject: "sub", Audience: testBundleID, Email: "x1y2@privaterelay.appleid.com", EmailVerified: true, PrivateEmail: true})
		_, user, err := svc.AppleLogin(context.Background(), input)
		if err != nil || len(users.created) != 1 {
			t.Fatalf("user=%+v err=%v", user, err)
		}
		created := users.created[0]
		if created.Name != "Ana Paz" || created.Username != "ana.paz" || created.Email != "x1y2@privaterelay.appleid.com" ||
			created.Role != "USER" || created.TermsAcceptedAt != nil || ids.subjects["sub"] != created.ID {
			t.Fatalf("created %+v", created)
		}
	})

	t.Run("wrong nonce", func(t *testing.T) {
		users := &appleUsersFake{byEmail: map[string]*domain.User{}, byID: map[string]*domain.User{}}
		ids := &appleIdentitiesFake{subjects: map[string]string{}, refresh: map[string]string{}}
		svc, _ := appleService(users, ids, domain.AppleClaims{Subject: "sub", Audience: testBundleID})
		bad := input
		bad.Nonce = "other"
		if _, _, err := svc.AppleLogin(context.Background(), bad); !errors.Is(err, domain.ErrInvalidAppleToken) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("deleted account", func(t *testing.T) {
		now := time.Now()
		gone := &domain.User{ID: "gone", DeletedAt: &now}
		users := &appleUsersFake{byEmail: map[string]*domain.User{}, byID: map[string]*domain.User{"gone": gone}}
		ids := &appleIdentitiesFake{subjects: map[string]string{"sub": "gone"}, refresh: map[string]string{}}
		svc, _ := appleService(users, ids, domain.AppleClaims{Subject: "sub", Audience: testBundleID})
		if _, _, err := svc.AppleLogin(context.Background(), input); !errors.Is(err, domain.ErrAccountUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		if _, _, err := NewAuthService(&appleUsersFake{}).AppleLogin(context.Background(), input); !errors.Is(err, domain.ErrAppleNotConfigured) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRevokeAppleUsesStoredToken(t *testing.T) {
	ids := &appleIdentitiesFake{subjects: map[string]string{}, refresh: map[string]string{"u": "refresh-u"}}
	svc, client := appleService(&appleUsersFake{}, ids, domain.AppleClaims{})
	if err := svc.RevokeApple(context.Background(), "u"); err != nil || len(client.revoked) != 1 || client.revoked[0] != "refresh-u" {
		t.Fatalf("err=%v revoked=%v", err, client.revoked)
	}
	if err := svc.RevokeApple(context.Background(), "no-token"); err != nil || len(client.revoked) != 1 {
		t.Fatalf("without token nothing is revoked: %v %v", err, client.revoked)
	}
}
