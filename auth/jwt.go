// Package auth ofrece verificación JWT local contra un endpoint JWKS, con
// caché de claves, refresco proactivo antes del TTL (DEC-056) y una gracia de
// 30 s como respaldo. El verificador NO conoce al proveedor de identidad: la
// URL del JWKS (servida por la red interna, ADR 32 §2) y el emisor se inyectan
// por configuración; ningún nombre ni SDK de IdP vive en este paquete.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	platformerrors "github.com/AI27-Tech/pf-pkg-platform-go/errors"
	"github.com/golang-jwt/jwt/v5"
)

// Audiencia es el valor de DOMINIO que decide portal y datos (DEC-054). No es
// el claim técnico `aud` del JWT: se lee de un claim personalizado que fija la
// composición de claims del IdP (mapper o extensión, ADR 32 §4) a partir del rol.
type Audiencia string

const (
	// AudienciaCliente corresponde al rol `cliente` (portal self-service).
	AudienciaCliente Audiencia = "Cliente"
	// AudienciaInterno corresponde a los otros siete roles (panel interno).
	AudienciaInterno Audiencia = "Interno"
)

// Claims representa los claims del JWT de AI27 extraídos del token.
type Claims struct {
	// Subject es el identificador del usuario (claim sub).
	Subject string
	// Rol es el único rol del usuario. El alcance (unidad/cliente) NO viaja en
	// el token: se resuelve en servidor (DEC-053).
	Rol string
	// TenantID es el identificador del tenant/organización.
	TenantID string
	// Audience es el claim técnico `aud` del JWT. NO autoriza nada (DEC-054);
	// se verifica contra la audiencia esperada, pero no decide portal ni datos.
	Audience string
	// Audiencia es el valor de dominio (Cliente | Interno) leído de un claim
	// personalizado. Es lo que decide portal y datos.
	Audiencia Audiencia
	// SesionID es el identificador de sesión de AI27, estable durante los refresh.
	SesionID string
	// Raw contiene todos los claims del token para uso posterior.
	Raw map[string]any
}

// Authenticator verifica una cadena de token y devuelve sus claims. Lo cumple
// *Verifier en producción y el stub de desarrollo de core (DEC-049), de modo
// que el Middleware común no distinga uno de otro.
type Authenticator interface {
	VerifyToken(ctx context.Context, tokenString string) (*Claims, error)
}

// RevocationChecker consulta si una sesión fue revocada (ADR 26 §3). Es
// opcional: si el middleware recibe uno, rechaza los tokens cuya sesión esté
// revocada. Su implementación (proyección alimentada por el broadcast) es de
// E0-T4; aquí solo va la costura.
type RevocationChecker interface {
	// Revocada devuelve true si la sesión fue revocada.
	Revocada(ctx context.Context, sesionID string) bool
}

// Verifier verifica JWTs contra un endpoint JWKS con caché y refresco proactivo.
type Verifier struct {
	jwksURL    string
	httpClient *http.Client

	mu          sync.RWMutex
	cachedKeys  map[string]*rsa.PublicKey
	cacheExpiry time.Time
	ttl         time.Duration

	// expectedAudience es la audiencia técnica (`aud`) que todo token debe cumplir.
	expectedAudience string

	// audienciaClaim es el nombre del claim personalizado que lleva la Audiencia
	// de dominio. Si está vacío, se usa "audiencia".
	audienciaClaim string

	// refrescoProactivo controla la goroutine de refresco anticipado.
	refrescoOnce sync.Once
	detener      chan struct{}
}

var _ Authenticator = (*Verifier)(nil)

// Opcion configura el Verifier.
type Opcion func(*Verifier)

// ConAudienciaClaim fija el nombre del claim personalizado que lleva la
// Audiencia de dominio (default "audiencia").
func ConAudienciaClaim(nombre string) Opcion {
	return func(v *Verifier) { v.audienciaClaim = nombre }
}

// NewVerifier crea un verificador JWT que obtiene claves de la JWKS URL dada.
// Las claves se cachean por el TTL dado y se refrescan proactivamente antes de
// vencer (DEC-056). Si el JWKS es inalcanzable y la caché venció hace más de
// 30 s, la verificación falla (fallo cerrado).
func NewVerifier(jwksURL, expectedAudience string, ttl time.Duration, opts ...Opcion) *Verifier {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	v := &Verifier{
		jwksURL: jwksURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		cachedKeys:       make(map[string]*rsa.PublicKey),
		ttl:              ttl,
		expectedAudience: expectedAudience,
		audienciaClaim:   "audiencia",
		detener:          make(chan struct{}),
	}
	for _, o := range opts {
		o(v)
	}
	return v
}

// IniciarRefrescoProactivo arranca una goroutine que refresca el JWKS antes de
// que venza el TTL (a ~80 % del TTL, con jitter para que las réplicas no
// refresquen a la vez). Si el refresco falla, reintenta con backoff mientras la
// caché siga vigente; la gracia de 30 s solo entra si la caché ya venció. Es
// idempotente. Detener se hace cancelando ctx.
func (v *Verifier) IniciarRefrescoProactivo(ctx context.Context) {
	v.refrescoOnce.Do(func() {
		go v.bucleRefresco(ctx)
	})
}

func (v *Verifier) bucleRefresco(ctx context.Context) {
	for {
		espera := v.esperaProximoRefresco()
		select {
		case <-ctx.Done():
			return
		case <-v.detener:
			return
		case <-time.After(espera):
		}

		refrescoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		keys, err := v.fetchKeys(refrescoCtx)
		cancel()
		if err == nil {
			v.mu.Lock()
			v.cachedKeys = keys
			v.cacheExpiry = time.Now().Add(v.ttl)
			v.mu.Unlock()
			continue
		}
		// Falló el refresco anticipado: reintentar pronto con backoff corto
		// mientras la caché siga vigente; si venció, el camino de VerifyToken
		// aplicará la gracia/fallo cerrado.
		select {
		case <-ctx.Done():
			return
		case <-v.detener:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// esperaProximoRefresco devuelve cuánto esperar hasta el siguiente refresco:
// ~80 % del tiempo restante de la caché, con jitter de hasta +10 % del TTL para
// que las réplicas no refresquen todas a la vez.
func (v *Verifier) esperaProximoRefresco() time.Duration {
	v.mu.RLock()
	expiry := v.cacheExpiry
	ttl := v.ttl
	v.mu.RUnlock()

	restante := time.Until(expiry)
	if restante <= 0 {
		restante = ttl
	}
	// Refrescar al ~80 % del tiempo restante deja ~20 % de margen antes de vencer.
	espera := time.Duration(float64(restante) * 0.8)
	// Jitter positivo acotado para desincronizar réplicas, sin pasarse del margen.
	maxJitter := int64(float64(ttl) * 0.1)
	if maxJitter > 0 {
		espera += time.Duration(rand.Int63n(maxJitter)) //nolint:gosec // jitter no criptográfico
	}
	// Piso mínimo para no entrar en un bucle caliente si el margen es diminuto.
	if espera < 10*time.Millisecond {
		espera = 10 * time.Millisecond
	}
	return espera
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
// Fallo cerrado: si el JWKS es inalcanzable y la caché venció hace más de 30 s,
// devuelve error. Dentro de la gracia de 30 s se aceptan claves obsoletas.
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
		// Gracia de respaldo (DEC-056): si hay claves obsoletas y la caché venció
		// hace menos de 30 s, usarlas. Es el respaldo, no el camino normal: el
		// refresco proactivo debería haber renovado antes de llegar aquí.
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
// Verifica: firma (contra JWKS), expiración y audiencia técnica.
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
	// Audience: claim técnico `aud`.
	if aud, ok := mapClaims["aud"].(string); ok {
		claims.Audience = aud
	} else if audSlice, ok := mapClaims["aud"].([]any); ok && len(audSlice) > 0 {
		if aud, ok := audSlice[0].(string); ok {
			claims.Audience = aud
		}
	}
	// Audiencia: valor de dominio del claim personalizado. Si el IdP no lo
	// emitió, se deriva del rol como respaldo (cliente → Cliente; resto → Interno).
	if a, ok := mapClaims[v.audienciaClaim].(string); ok && a != "" {
		claims.Audiencia = Audiencia(a)
	} else {
		claims.Audiencia = audienciaDesdeRol(claims.Rol)
	}
	if sid, ok := mapClaims["sesion_id"].(string); ok {
		claims.SesionID = sid
	}

	return claims, nil
}

// audienciaDesdeRol mapea el rol a la Audiencia de dominio (DEC-054): solo
// `cliente` es Cliente; los otros siete roles (incluido `reaccion`) son Interno.
func audienciaDesdeRol(rol string) Audiencia {
	if rol == "cliente" {
		return AudienciaCliente
	}
	return AudienciaInterno
}

// Middleware devuelve un middleware HTTP que verifica el header Authorization
// con el Authenticator dado e inyecta los Claims en el contexto. Las respuestas
// de error usan la taxonomía de la plataforma (I3) con la forma `Error` de
// `_comunes.yaml` (`codigo`, `mensaje`). Si se pasa un RevocationChecker, se
// rechaza la sesión revocada.
func Middleware(a Authenticator, rev RevocationChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				escribirNoAutorizado(w, "header de autorización faltante")
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader {
				escribirNoAutorizado(w, "formato de autorización inválido")
				return
			}

			claims, err := a.VerifyToken(r.Context(), tokenString)
			if err != nil {
				escribirNoAutorizado(w, "token inválido")
				return
			}

			if rev != nil && claims.SesionID != "" && rev.Revocada(r.Context(), claims.SesionID) {
				escribirNoAutorizado(w, "sesión revocada")
				return
			}

			next.ServeHTTP(w, r.WithContext(ContextWithClaims(r.Context(), claims)))
		})
	}
}

// Middleware (método) se conserva por compatibilidad: equivale a la función
// Middleware sin consulta de revocación.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return Middleware(v, nil)(next)
}

// escribirNoAutorizado responde 401 con la forma Error de la plataforma.
func escribirNoAutorizado(w http.ResponseWriter, mensaje string) {
	cat := platformerrors.CategoryNoAutorizado
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(cat.HTTPStatus())
	_ = json.NewEncoder(w).Encode(map[string]string{
		"codigo":  cat.String(),
		"mensaje": mensaje,
	})
}

// claimsKey es la clave del contexto para los claims.
type claimsKey struct{}

// ContextWithClaims devuelve un contexto que lleva los claims dados. Lo usa el
// middleware y cualquier costura que inyecte claims (p. ej. el stub de
// desarrollo de core, DEC-049).
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

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
