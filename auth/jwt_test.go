package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testKid = "clave-de-prueba-1"
	testAud = "ai27-interno"
)

// jwksServer arma un servidor JWKS de prueba que sirve la clave pública dada.
// Devuelve el servidor y un puntero atómico para simular caídas (503).
func jwksServer(t *testing.T, kid string, pub *rsa.PublicKey) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	caido := &atomic.Bool{}

	body := jwksResponse{Keys: []jwksKey{{
		Kid: kid,
		Kty: "RSA",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caido.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, caido
}

// firmarToken firma un JWT RS256 con el kid y claims dados.
func firmarToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return s
}

func claimsBase() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":       "usuario-1",
		"rol":       "operator",
		"tenant_id": "tenant-1",
		"aud":       testAud,
		"sesion_id": "sesion-1",
		"exp":       time.Now().Add(time.Hour).Unix(),
		"iat":       time.Now().Add(-time.Minute).Unix(),
	}
}

func nuevaClave(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generar clave RSA: %v", err)
	}
	return key
}

func TestVerifyToken_Valido(t *testing.T) {
	key := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)

	tok := firmarToken(t, key, testKid, claimsBase())
	claims, err := v.VerifyToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("token válido debería verificar: %v", err)
	}

	// Extracción de claims.
	if claims.Subject != "usuario-1" {
		t.Errorf("Subject = %q, se esperaba usuario-1", claims.Subject)
	}
	if claims.Rol != "operator" {
		t.Errorf("Rol = %q, se esperaba operator", claims.Rol)
	}
	if claims.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, se esperaba tenant-1", claims.TenantID)
	}
	if claims.Audiencia != testAud {
		t.Errorf("Audiencia = %q, se esperaba %q", claims.Audiencia, testAud)
	}
	if claims.SesionID != "sesion-1" {
		t.Errorf("SesionID = %q, se esperaba sesion-1", claims.SesionID)
	}
}

func TestVerifyToken_FirmaInvalida(t *testing.T) {
	// El JWKS publica una clave, pero el token se firma con OTRA.
	keyBuena := nuevaClave(t)
	keyMala := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &keyBuena.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)

	tok := firmarToken(t, keyMala, testKid, claimsBase())
	if _, err := v.VerifyToken(context.Background(), tok); err == nil {
		t.Fatal("un token con firma inválida no debería verificar")
	}
}

func TestVerifyToken_Expirado(t *testing.T) {
	key := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)

	claims := claimsBase()
	claims["exp"] = time.Now().Add(-time.Hour).Unix() // expirado
	tok := firmarToken(t, key, testKid, claims)
	if _, err := v.VerifyToken(context.Background(), tok); err == nil {
		t.Fatal("un token expirado no debería verificar")
	}
}

func TestVerifyToken_AudienciaIncorrecta(t *testing.T) {
	key := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)

	claims := claimsBase()
	claims["aud"] = "otra-audiencia"
	tok := firmarToken(t, key, testKid, claims)
	if _, err := v.VerifyToken(context.Background(), tok); err == nil {
		t.Fatal("un token con audiencia incorrecta no debería verificar")
	}
}

func TestVerifyToken_KidDesconocido(t *testing.T) {
	key := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)

	tok := firmarToken(t, key, "kid-que-no-existe", claimsBase())
	if _, err := v.VerifyToken(context.Background(), tok); err == nil {
		t.Fatal("un token con kid desconocido no debería verificar")
	}
}

func TestVerifyToken_SinKid(t *testing.T) {
	key := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)

	// Firmar sin header kid.
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claimsBase())
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("firmar: %v", err)
	}
	if _, err := v.VerifyToken(context.Background(), s); err == nil {
		t.Fatal("un token sin kid no debería verificar")
	}
}

// TestVerifyToken_FailClosed es el caso de seguridad más importante: si el JWKS
// es inalcanzable y la caché expiró, la verificación DEBE rechazar, no aceptar.
func TestVerifyToken_FailClosed(t *testing.T) {
	key := nuevaClave(t)
	srv, caido := jwksServer(t, testKid, &key.PublicKey)
	// TTL muy corto para que la caché expire de inmediato.
	v := NewVerifier(srv.URL, testAud, time.Nanosecond)

	tok := firmarToken(t, key, testKid, claimsBase())

	// Primera verificación: JWKS disponible, funciona y cachea.
	if _, err := v.VerifyToken(context.Background(), tok); err != nil {
		t.Fatalf("primera verificación debería funcionar: %v", err)
	}

	// El JWKS "cae" y esperamos a que pase la ventana de gracia (30s) del código.
	caido.Store(true)
	// Forzamos expiración de la caché y de la gracia manipulando el estado interno.
	v.mu.Lock()
	v.cacheExpiry = time.Now().Add(-time.Hour) // caché vencida hace 1h (> 30s de gracia)
	v.mu.Unlock()

	if _, err := v.VerifyToken(context.Background(), tok); err == nil {
		t.Fatal("con JWKS caído y caché vencida (fuera de gracia), debe rechazar (fail-closed)")
	}
}

// TestVerifyToken_GraciaConJWKSCaido comprueba que dentro de la ventana de
// gracia sí se aceptan claves obsoletas (disponibilidad ante fallos transitorios).
func TestVerifyToken_GraciaConJWKSCaido(t *testing.T) {
	key := nuevaClave(t)
	srv, caido := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Nanosecond)

	tok := firmarToken(t, key, testKid, claimsBase())
	if _, err := v.VerifyToken(context.Background(), tok); err != nil {
		t.Fatalf("primera verificación debería funcionar: %v", err)
	}

	caido.Store(true)
	// Caché vencida hace 5s: dentro de la gracia de 30s -> debe seguir aceptando.
	v.mu.Lock()
	v.cacheExpiry = time.Now().Add(-5 * time.Second)
	v.mu.Unlock()

	if _, err := v.VerifyToken(context.Background(), tok); err != nil {
		t.Fatalf("dentro de la gracia debería aceptar claves obsoletas: %v", err)
	}
}

func TestMiddleware(t *testing.T) {
	key := nuevaClave(t)
	srv, _ := jwksServer(t, testKid, &key.PublicKey)
	v := NewVerifier(srv.URL, testAud, time.Minute)
	tokValido := firmarToken(t, key, testKid, claimsBase())

	// Handler protegido que confirma que los claims llegaron al contexto.
	var claimsVistos *Claims
	protegido := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claimsVistos = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	casos := []struct {
		nombre     string
		header     string
		wantStatus int
	}{
		{"con token válido", "Bearer " + tokValido, http.StatusOK},
		{"sin header", "", http.StatusUnauthorized},
		{"sin prefijo Bearer", tokValido, http.StatusUnauthorized},
		{"token basura", "Bearer no-es-un-jwt", http.StatusUnauthorized},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			claimsVistos = nil
			req := httptest.NewRequest(http.MethodGet, "/protegido", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			protegido.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, se esperaba %d", rec.Code, c.wantStatus)
			}
			if c.wantStatus == http.StatusOK {
				if claimsVistos == nil {
					t.Error("los claims deberían estar en el contexto del handler")
				} else if claimsVistos.SesionID != "sesion-1" {
					t.Errorf("SesionID en contexto = %q, se esperaba sesion-1", claimsVistos.SesionID)
				}
			}
		})
	}
}

func TestClaimsFromContext_Vacio(t *testing.T) {
	if c := ClaimsFromContext(context.Background()); c != nil {
		t.Errorf("sin claims en contexto debería devolver nil, got %+v", c)
	}
}
