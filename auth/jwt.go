// Package auth ofrece verificación JWT local contra el JWKS de Cognito,
// con caché de claves con TTL y fallo cerrado si el JWKS es inalcanzable
// y la caché expiró.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims representa los claims del JWT de AI27 extraídos del token.
type Claims struct {
	// Subject es el identificador del usuario (claim sub).
	Subject string
	// Rol es el rol del usuario dentro del tenant.
	Rol string
	// TenantID es el identificador del tenant/organización.
	TenantID string
	// Audiencia es el claim aud — debe coincidir con la audiencia esperada, nunca se acepta como parámetro de petición (P10).
	Audiencia string
	// SesionID es el identificador de sesión de AI27, estable durante todos los refresh.
	SesionID string
	// Raw contiene todos los claims del token para uso posterior.
	Raw map[string]any
}

// Verifier verifica JWTs contra un endpoint JWKS con caché.
type Verifier struct {
	jwksURL    string
	httpClient *http.Client

	mu          sync.RWMutex
	cachedKeys  map[string]*rsa.PublicKey
	cacheExpiry time.Time
	ttl         time.Duration

	// expectedAudience es la audiencia que todos los tokens deben cumplir.
	expectedAudience string
}

// NewVerifier crea un verificador JWT que obtiene claves de la JWKS URL dada.
// Las claves se cachean por el TTL dado. Si el JWKS es inalcanzable y la caché
// expiró, la verificación falla (fallo cerrado).
func NewVerifier(jwksURL, expectedAudience string, ttl time.Duration) *Verifier {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &Verifier{
		jwksURL: jwksURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		cachedKeys:       make(map[string]*rsa.PublicKey),
		ttl:              ttl,
		expectedAudience: expectedAudience,
	}
}

// jwksResponse es la estructura JSON que devuelve el endpoint JWKS.
type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

type jwksKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// fetchKeys obtiene las claves del endpoint JWKS remoto.
func (v *Verifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: crear petición JWKS: %w", err)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: obtener JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: el endpoint JWKS devolvió %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth: leer cuerpo JWKS: %w", err)
	}

	var jwks jwksResponse
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, fmt.Errorf("auth: parsear JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty == "RSA" && k.Alg == "RS256" {
			pubKey, err := parseRSAPublicKey(k.N, k.E)
			if err != nil {
				continue // omitir claves que no se pueden parsear
			}
			keys[k.Kid] = pubKey
		}
	}

	return keys, nil
}

// getKeys devuelve las claves cacheadas, refrescándolas si expiraron.
// Fallo cerrado: si el JWKS es inalcanzable y la caché expiró, devuelve error.
func (v *Verifier) getKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	v.mu.RLock()
	if v.cacheExpiry.After(time.Now()) && len(v.cachedKeys) > 0 {
		keys := v.cachedKeys
		v.mu.RUnlock()
		return keys, nil
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()

	// Doble verificación después de adquirir el lock de escritura.
	if v.cacheExpiry.After(time.Now()) && len(v.cachedKeys) > 0 {
		return v.cachedKeys, nil
	}

	keys, err := v.fetchKeys(ctx)
	if err != nil {
		// Si tenemos claves obsoletas y el error es de red, usar claves obsoletas
		// pero solo si la caché no lleva mucho tiempo expirada (máx 30s de gracia).
		if len(v.cachedKeys) > 0 && time.Since(v.cacheExpiry) < 30*time.Second {
			return v.cachedKeys, nil
		}
		// Fallo cerrado: no hay claves usables.
		return nil, fmt.Errorf("auth: JWKS no disponible y caché expirada: %w", err)
	}

	v.cachedKeys = keys
	v.cacheExpiry = time.Now().Add(v.ttl)
	return keys, nil
}

// VerifyToken parsea y verifica una cadena JWT, devolviendo los claims extraídos.
// Verifica: firma (contra JWKS), expiración y audiencia.
func (v *Verifier) VerifyToken(ctx context.Context, tokenString string) (*Claims, error) {
	// Parsear el header del token para obtener el kid.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("auth: parsear token: %w", err)
	}

	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, fmt.Errorf("auth: token sin header kid")
	}

	// Obtener la clave de firma.
	keys, err := v.getKeys(ctx)
	if err != nil {
		return nil, err
	}

	pubKey, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("auth: kid desconocido %q", kid)
	}

	// Parsear y verificar con la clave.
	token, err = jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("auth: método de firma inesperado %v", t.Header["alg"])
		}
		return pubKey, nil
	},
		jwt.WithExpirationRequired(),
		jwt.WithAudience(v.expectedAudience),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: verificar token: %w", err)
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("auth: tipo de claims inválido")
	}

	claims := &Claims{
		Raw: make(map[string]any, len(mapClaims)),
	}

	for k, val := range mapClaims {
		claims.Raw[k] = val
	}

	if sub, ok := mapClaims["sub"].(string); ok {
		claims.Subject = sub
	}
	if rol, ok := mapClaims["rol"].(string); ok {
		claims.Rol = rol
	}
	if tenant, ok := mapClaims["tenant_id"].(string); ok {
		claims.TenantID = tenant
	}
	if aud, ok := mapClaims["aud"].(string); ok {
		claims.Audiencia = aud
	} else if audSlice, ok := mapClaims["aud"].([]any); ok && len(audSlice) > 0 {
		if aud, ok := audSlice[0].(string); ok {
			claims.Audiencia = aud
		}
	}
	if sid, ok := mapClaims["sesion_id"].(string); ok {
		claims.SesionID = sid
	}

	return claims, nil
}

// Middleware devuelve un middleware HTTP que verifica el header Authorization
// e inyecta los Claims en el contexto de la petición.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"header de autorización faltante"}`, http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			http.Error(w, `{"error":"formato de autorización inválido"}`, http.StatusUnauthorized)
			return
		}

		claims, err := v.VerifyToken(r.Context(), tokenString)
		if err != nil {
			http.Error(w, `{"error":"token inválido"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), claimsKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// claimsKey es la clave del contexto para los claims.
type claimsKey struct{}

// ClaimsFromContext extrae los claims del JWT del contexto de la petición.
// Devuelve nil si no hay claims presentes.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsKey{}).(*Claims)
	return claims
}

// parseRSAPublicKey parsea una clave pública RSA desde módulo y exponente (base64url).
func parseRSAPublicKey(n, e string) (*rsa.PublicKey, error) {
	// El parseo de claves JWT usa bytes en formato big-endian.
	pubKey := &rsa.PublicKey{}

	// Decodificar módulo.
	nBytes, err := base64URLDecode(n)
	if err != nil {
		return nil, fmt.Errorf("decodificar n: %w", err)
	}
	pubKey.N = bytesToBigInt(nBytes)

	// Decodificar exponente.
	eBytes, err := base64URLDecode(e)
	if err != nil {
		return nil, fmt.Errorf("decodificar e: %w", err)
	}
	pubKey.E = bytesToInt(eBytes)

	return pubKey, nil
}
