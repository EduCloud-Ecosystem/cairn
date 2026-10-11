// SPDX-License-Identifier: AGPL-3.0-or-later
package lti_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/lti"
	"github.com/EduCloud-Ecosystem/cairn/internal/ltisim"
	"github.com/golang-jwt/jwt/v5"
)

func fixture(t *testing.T) (*lti.Client, *ltisim.Simulator) {
	t.Helper()
	platform, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	tool, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	sim, e := ltisim.New(base, "http://127.0.0.1:8080", platform, &tool.PublicKey)
	if e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = sim.Handler()
	server.Start()
	t.Cleanup(server.Close)
	client, e := lti.New(sim.Config("course", ""), tool)
	if e != nil {
		t.Fatal(e)
	}
	return client, sim
}
func TestSignedLaunchSecurity(t *testing.T) {
	c, s := fixture(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*lti.LaunchClaims)
		nonce  string
		valid  bool
	}{
		{"valid learner", nil, "nonce", true},
		{"wrong nonce", nil, "other", false},
		{"wrong issuer", func(v *lti.LaunchClaims) { v.Issuer = "https://attacker.invalid" }, "nonce", false},
		{"wrong audience", func(v *lti.LaunchClaims) { v.Audience = jwt.ClaimStrings{"other"} }, "nonce", false},
		{"multi audience missing azp", func(v *lti.LaunchClaims) { v.Audience = append(v.Audience, "other") }, "nonce", false},
		{"wrong azp", func(v *lti.LaunchClaims) { v.AuthorizedParty = "other" }, "nonce", false},
		{"expired", func(v *lti.LaunchClaims) { v.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }, "nonce", false},
		{"future issued", func(v *lti.LaunchClaims) { v.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour)) }, "nonce", false},
		{"missing expiry", func(v *lti.LaunchClaims) { v.ExpiresAt = nil }, "nonce", false},
		{"wrong deployment", func(v *lti.LaunchClaims) { v.DeploymentID = "other" }, "nonce", false},
		{"wrong target", func(v *lti.LaunchClaims) { v.Target = "https://attacker.invalid" }, "nonce", false},
		{"wrong message", func(v *lti.LaunchClaims) { v.MessageType = "LtiDeepLinkingRequest" }, "nonce", false},
		{"no subject", func(v *lti.LaunchClaims) { v.Subject = "" }, "nonce", false},
		{"ambiguous role", func(v *lti.LaunchClaims) { v.Roles = append(v.Roles, lti.Instructor) }, "nonce", false},
		{"institution role", func(v *lti.LaunchClaims) {
			v.Roles = []string{"http://purl.imsglobal.org/vocab/lis/v2/institution/person#Instructor"}
		}, "nonce", false},
		{"untrusted endpoint", func(v *lti.LaunchClaims) { v.Endpoint.LineItem = "http://127.0.0.1:9/admin" }, "nonce", false},
		{"query endpoint", func(v *lti.LaunchClaims) { v.Endpoint.LineItem += "?redirect=secret" }, "nonce", false},
		{"missing score scope", func(v *lti.LaunchClaims) { v.Endpoint.Scope = v.Endpoint.Scope[1:] }, "nonce", false},
		{"full lineitem scope", func(v *lti.LaunchClaims) { v.Endpoint.Scope[2] = lti.AGS + "scope/lineitem" }, "nonce", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, e := s.Token("nonce", "student-a", tc.mutate)
			if e != nil {
				t.Fatal(e)
			}
			v, e := c.Verify(ctx, raw, tc.nonce)
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, e)
			}
			if e == nil && (v.Subject != "student-a" || v.Role != lti.Learner) {
				t.Fatal("wrong identity")
			}
		})
	}
	raw, e := s.Token("nonce", "student-a", nil)
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(raw, ".")
	parts[1] = "eyJzdWIiOiJhdHRhY2tlciJ9"
	if _, e = c.Verify(ctx, strings.Join(parts, "."), "nonce"); e == nil {
		t.Fatal("tampered launch accepted")
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "attacker"})
	raw, _ = unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, e = c.Verify(ctx, raw, "nonce"); e == nil {
		t.Fatal("unsigned launch accepted")
	}
}
func TestSigningKeyPermissionsAndConfig(t *testing.T) {
	c, _ := fixture(t)
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	cfg := c.Config
	cfg.PrivateKeyFile = path
	raw, _ := json.Marshal(cfg)
	file := filepath.Join(dir, "lti.json")
	os.WriteFile(file, raw, 0600)
	if _, e = lti.Load(file); e != nil {
		t.Fatal(e)
	}
	os.Chmod(path, 0644)
	if _, e = lti.Load(file); e == nil {
		t.Fatal("world-readable signing key accepted")
	}
	for _, change := range []func(*lti.Config){func(c *lti.Config) { c.Simulation = false }, func(c *lti.Config) { c.BaseURL = "http://0.0.0.0:8080" }, func(c *lti.Config) { c.ServiceOrigin += "/path" }, func(c *lti.Config) { c.Classrooms = nil }, func(c *lti.Config) { c.JWKSURL = "http://example.com/keys" }} {
		cfg := c.Config
		change(&cfg)
		if _, e = lti.New(cfg, key); e == nil {
			t.Fatal("unsafe configuration accepted", cfg)
		}
	}
}
func TestJWKSRedirectAndMalformedKeysFailClosed(t *testing.T) {
	c, s := fixture(t)
	raw, _ := s.Token("nonce", "student-a", nil)
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed untrusted JWKS redirect") }))
	defer secret.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, secret.URL, http.StatusFound) }))
	defer redirect.Close()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	cfg := c.Config
	cfg.JWKSURL = redirect.URL
	c, e := lti.New(cfg, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Verify(context.Background(), raw, "nonce"); e == nil {
		t.Fatal("redirect accepted")
	}
}
func FuzzServiceURL(f *testing.F) {
	for _, s := range []string{"https://lms.example/ags/1", "https://lms.example.evil/ags/1", "https://lms.example@evil/ags", "https://lms.example/../secret", "https://lms.example/%2fsecret", "http://127.0.0.1:80/", "https://lms.example/ags?token=1"} {
		f.Add(s)
	}
	c := &lti.Client{Config: lti.Config{ServiceOrigin: "https://lms.example"}}
	f.Fuzz(func(t *testing.T, s string) {
		if c.ServiceURL(s) && (!strings.HasPrefix(s, "https://lms.example/") || strings.ContainsAny(s, "?#\r\n")) {
			t.Fatalf("unsafe endpoint accepted: %q", s)
		}
	})
}
