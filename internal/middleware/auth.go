package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// authContextKey is a private type for auth-specific context keys.
type authContextKey string

const userIDKey authContextKey = "userID"

// GetUserID retrieves the authenticated user's subject claim from the context.
// Returns an empty string if auth is disabled or no JWT was validated.
func GetUserID(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

// JWTValidator holds the JWKS cache and validation options.
// It uses lestrrat-go/jwx to automatically fetch and cache public keys
// from the JWKS endpoint (e.g., https://auth-server/.well-known/jwks.json).
type JWTValidator struct {
	keyCache *jwk.Cache
	jwksURL  string
	issuer   string
	audience string
	log      *slog.Logger
}

// NewJWTValidator creates a validator that fetches public keys from the JWKS URL.
// Keys are cached and automatically refreshed in the background.
func NewJWTValidator(ctx context.Context, jwksURL, issuer, audience string, log *slog.Logger) (*JWTValidator, error) {
	cache := jwk.NewCache(ctx)

	// Register the JWKS URL with auto-refresh every 15 minutes.
	// This means the gateway will update public keys automatically if the auth
	// server rotates its signing keys — zero downtime key rotation.
	if err := cache.Register(jwksURL, jwk.WithMinRefreshInterval(15*time.Minute)); err != nil {
		return nil, fmt.Errorf("registering jwks url: %w", err)
	}

	// Pre-warm the cache immediately so startup failures are visible early.
	if _, err := cache.Refresh(ctx, jwksURL); err != nil {
		// Log as a warning — don't fail startup if the JWKS server is temporarily unreachable.
		log.Warn("failed to pre-warm JWKS cache, will retry on first request",
			slog.String("jwks_url", jwksURL),
			slog.String("error", err.Error()),
		)
	}

	return &JWTValidator{
		keyCache: cache,
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
		log:      log,
	}, nil
}

// validate parses and cryptographically verifies a JWT string.
// It checks:
//  1. Signature validity using the JWKS public key
//  2. exp (expiry) — rejects expired tokens
//  3. iss (issuer) — ensures token was issued by the trusted auth server
//  4. aud (audience) — ensures token was meant for this gateway
func (v *JWTValidator) validate(ctx context.Context, tokenString string) (jwt.Token, error) {
	keySet, err := v.keyCache.Get(ctx, v.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetching jwks: %w", err)
	}

	opts := []jwt.ParseOption{
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}

	token, err := jwt.ParseString(tokenString, opts...)
	if err != nil {
		return nil, fmt.Errorf("invalid jwt: %w", err)
	}
	return token, nil
}

// Auth returns an HTTP middleware. It handles two modes based on configuration:
//
//  1. JWT Mode (jwtValidator != nil): Cryptographically validates a signed RS256 JWT.
//     Validates exp, iss, aud claims. Injects user ID into the request context.
//  2. Static Token Mode (fallback): Simple bearer token string match from config.yaml.
func Auth(log *slog.Logger, validTokens []string, enabled bool, jwtValidator *JWTValidator) func(http.Handler) http.Handler {
	// Build a static token set for O(1) lookup (fallback mode)
	tokenSet := make(map[string]struct{}, len(validTokens))
	for _, t := range validTokens {
		tokenSet[t] = struct{}{}
	}

	skipPaths := map[string]bool{
		"/health":  true,
		"/metrics": true,
		"/docs/":   true,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip whitelisted paths
			for skip := range skipPaths {
				if strings.HasPrefix(r.URL.Path, skip) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Extract bearer token from "Authorization: Bearer <token>"
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				log.Warn("auth: missing Authorization header",
					slog.String("request_id", GetRequestID(r.Context())),
					slog.String("path", r.URL.Path),
				)
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"unauthorized","message":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"unauthorized","message":"authorization must be Bearer <token>"}`, http.StatusUnauthorized)
				return
			}
			rawToken := parts[1]

			// JWT Mode: cryptographic validation
			if jwtValidator != nil {
				token, err := jwtValidator.validate(r.Context(), rawToken)
				if err != nil {
					log.Warn("auth: invalid JWT",
						slog.String("request_id", GetRequestID(r.Context())),
						slog.String("path", r.URL.Path),
						slog.String("error", err.Error()),
					)
					w.Header().Set("Content-Type", "application/json")
					http.Error(w, `{"error":"forbidden","message":"invalid or expired token"}`, http.StatusForbidden)
					return
				}

				// Inject the user's subject (user ID) into the context.
				// The rate limiter uses this as the identity key.
				ctx := context.WithValue(r.Context(), userIDKey, token.Subject())
				r = r.WithContext(ctx)

				log.Info("auth: JWT validated",
					slog.String("request_id", GetRequestID(r.Context())),
					slog.String("subject", token.Subject()),
				)
				next.ServeHTTP(w, r)
				return
			}

			// Static token fallback mode
			if _, ok := tokenSet[rawToken]; !ok {
				log.Warn("auth: invalid static token",
					slog.String("request_id", GetRequestID(r.Context())),
					slog.String("path", r.URL.Path),
				)
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
