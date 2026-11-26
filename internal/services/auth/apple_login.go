package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

// appleRevoker es lo que AppleLogin necesita además de verificar: canjear y revocar refresh tokens.
type appleRevoker interface {
	CanRevoke() bool
}

// UseApple habilita Sign in with Apple. Sin esto, AppleLogin responde domain.ErrAppleNotConfigured.
func (s *authService) UseApple(client ports.AppleAuthClient, identities ports.AppleIdentityRepository) {
	s.apple, s.appleIdentities = client, identities
}

// HashAppleNonce es el valor que se le manda a Apple como nonce (SHA-256 en hex del nonce original).
func HashAppleNonce(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

// AppleLogin verifica el identityToken y entra con la cuenta vinculada a ese Apple ID. Si no hay, vincula la cuenta
// con el mismo email verificado (no de relay) o crea una nueva, que completa el perfil y acepta los términos después.
func (s *authService) AppleLogin(ctx context.Context, input domain.AppleLoginInput) (string, *domain.User, error) {
	if s.apple == nil || s.appleIdentities == nil {
		return "", nil, domain.ErrAppleNotConfigured
	}
	if strings.TrimSpace(input.IdentityToken) == "" || strings.TrimSpace(input.Nonce) == "" {
		return "", nil, domain.ErrInvalidAppleToken
	}
	claims, err := s.apple.Verify(ctx, input.IdentityToken, HashAppleNonce(input.Nonce))
	if err != nil {
		return "", nil, err
	}

	user, err := s.appleAccount(ctx, claims, input)
	if err != nil {
		return "", nil, err
	}
	if user.Unavailable() {
		return "", nil, domain.ErrAccountUnavailable
	}

	// El refresh token permite revocar el acceso cuando la persona borra su cuenta (lo exige Apple). Si el canje
	// falla, el login sigue: se reintenta en el próximo inicio de sesión.
	if code := strings.TrimSpace(input.AuthorizationCode); code != "" {
		if revoker, ok := s.apple.(appleRevoker); !ok || revoker.CanRevoke() {
			exchangeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			refreshToken, err := s.apple.Exchange(exchangeCtx, code, claims.Audience)
			cancel()
			if err != nil {
				log.Printf("apple login %s: %v", user.ID, err)
			} else if err := s.appleIdentities.SetRefreshToken(ctx, user.ID, claims.Audience, refreshToken); err != nil {
				log.Printf("apple login %s: saving refresh token: %v", user.ID, err)
			}
		}
	}

	token, err := s.issueSession(ctx, user)
	if err != nil {
		return "", nil, err
	}
	return token, user, nil
}

func (s *authService) appleAccount(ctx context.Context, claims domain.AppleClaims, input domain.AppleLoginInput) (*domain.User, error) {
	// 1. Ya vinculada con este Apple ID.
	userID, err := s.appleIdentities.UserIDBySubject(ctx, claims.Subject)
	if err == nil {
		return s.userRepo.GetUserByID(ctx, userID)
	}
	if !errors.Is(err, domain.ErrUserNotFound) {
		return nil, err
	}

	// 2. Misma persona que ya tenía cuenta con ese email (verificado por Apple y no de relay).
	email := strings.ToLower(claims.Email)
	if email != "" && claims.EmailVerified && !claims.PrivateEmail {
		if existing, err := s.userRepo.GetUserByEmail(ctx, email); err == nil && existing != nil {
			if existing.Unavailable() {
				return nil, domain.ErrAccountUnavailable
			}
			if err := s.appleIdentities.Link(ctx, existing.ID, claims.Subject, claims.Audience, email); err != nil {
				return nil, err
			}
			return existing, nil
		}
	}

	// 3. Cuenta nueva. Apple manda el nombre solo la primera vez; si no viene, se completa en /complete-profile.
	if email == "" {
		email = "apple+" + HashAppleNonce(claims.Subject)[:16] + "@privaterelay.invalid"
	} else if _, err := s.userRepo.GetUserByEmail(ctx, email); err == nil {
		// Ese email ya es de otra cuenta y Apple no lo verificó: no se puede reutilizar.
		email = "apple+" + HashAppleNonce(claims.Subject)[:16] + "@privaterelay.invalid"
	}
	name := strings.TrimSpace(strings.TrimSpace(input.GivenName) + " " + strings.TrimSpace(input.FamilyName))
	usernameBase := strings.Split(email, "@")[0]
	if claims.PrivateEmail || strings.HasSuffix(email, ".invalid") {
		usernameBase = strings.ReplaceAll(strings.ToLower(name), " ", ".")
	}
	now := time.Now()
	user := &domain.User{
		ID:           uuid.NewString(),
		Email:        email,
		Name:         name,
		Username:     s.uniqueUsername(ctx, usernameBase),
		PasswordHash: "[APPLE_AUTH]",
		Role:         "USER",
		Followers:    []string{},
		Following:    []string{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("creating apple account: %w", err)
	}
	if err := s.appleIdentities.Link(ctx, user.ID, claims.Subject, claims.Audience, claims.Email); err != nil {
		return nil, err
	}
	return user, nil
}

// RevokeApple revoca el acceso de Apple de la cuenta (al borrarla). Sin vínculo o sin clave configurada no hace nada.
func (s *authService) RevokeApple(ctx context.Context, userID string) error {
	if s.apple == nil || s.appleIdentities == nil {
		return nil
	}
	refreshToken, clientID, err := s.appleIdentities.RefreshToken(ctx, userID)
	if err != nil || refreshToken == "" {
		return err
	}
	revokeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.apple.Revoke(revokeCtx, refreshToken, clientID)
}
