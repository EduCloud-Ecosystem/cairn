// SPDX-License-Identifier: AGPL-3.0-or-later
// Package lti implements a bounded, opt-in LTI 1.3/AGS client for Brightspace.
package lti

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/golang-jwt/jwt/v5"
)

const Claim = "https://purl.imsglobal.org/spec/lti/claim/"
const AGS = "https://purl.imsglobal.org/spec/lti-ags/"
const Instructor = "http://purl.imsglobal.org/vocab/lis/v2/membership#Instructor"
const Learner = "http://purl.imsglobal.org/vocab/lis/v2/membership#Learner"

var ErrInvalid = errors.New("LTI configuration, launch or service response is invalid")
var ErrRemote = errors.New("LMS service unavailable; delivery has not been verified")

// Endpoint URLs are operator-trusted configuration, never taken from request
// parameters or JWT jku headers. Simulation permits HTTP on 127.0.0.1 only.
type Config struct {
	Issuer         string   `json:"issuer"`
	ClientID       string   `json:"client_id"`
	DeploymentID   string   `json:"deployment_id"`
	AuthURL        string   `json:"auth_url"`
	TokenURL       string   `json:"token_url"`
	TokenAudience  string   `json:"token_audience"`
	JWKSURL        string   `json:"jwks_url"`
	ServiceOrigin  string   `json:"service_origin"`
	BaseURL        string   `json:"base_url"`
	KeyID          string   `json:"key_id"`
	PrivateKeyFile string   `json:"private_key_file"`
	Classrooms     []string `json:"classrooms"`
	Simulation     bool     `json:"simulation"`
}
type Client struct {
	Config       Config
	key          *rsa.PrivateKey
	http         *http.Client
	Registration string
}

func Load(path string) (*Client, error) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 65536 {
		return nil, ErrInvalid
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&cfg) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	info, err := os.Stat(cfg.PrivateKeyFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return nil, errors.New("LTI signing key requires a private regular PEM file (mode 0600)")
	}
	pem, err := os.ReadFile(cfg.PrivateKeyFile)
	if err != nil {
		return nil, ErrInvalid
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
	if err != nil {
		return nil, ErrInvalid
	}
	return New(cfg, key)
}
func New(cfg Config, key *rsa.PrivateKey) (*Client, error) {
	if key == nil || key.N.BitLen() < 2048 || cfg.ClientID == "" || cfg.DeploymentID == "" || cfg.KeyID == "" || cfg.TokenAudience == "" || len(cfg.Classrooms) == 0 {
		return nil, ErrInvalid
	}
	for _, raw := range []string{cfg.Issuer, cfg.AuthURL, cfg.TokenURL, cfg.JWKSURL, cfg.ServiceOrigin, cfg.BaseURL} {
		if !validURL(raw, cfg.Simulation) {
			return nil, ErrInvalid
		}
	}
	for _, raw := range []string{cfg.ServiceOrigin, cfg.BaseURL} {
		u, _ := url.Parse(raw)
		if u.Path != "" || u.RawQuery != "" {
			return nil, ErrInvalid
		}
	}
	safe := cfg
	safe.PrivateKeyFile = ""
	safe.KeyID = ""
	safe.Classrooms = nil
	raw, _ := json.Marshal(safe)
	sum := sha256.Sum256(raw)
	return &Client{Config: cfg, key: key, Registration: hex.EncodeToString(sum[:]), http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func validURL(raw string, simulation bool) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "#\r\n") {
		return false
	}
	if simulation {
		return u.Scheme == "http" && u.Hostname() == "127.0.0.1"
	}
	return u.Scheme == "https"
}
func (c *Client) AllowedClassroom(id string) bool {
	for _, v := range c.Config.Classrooms {
		if id == v {
			return true
		}
	}
	return false
}
func (c *Client) ServiceURL(raw string) bool {
	if !validURL(raw, c.Config.Simulation) {
		return false
	}
	u, _ := url.Parse(raw)
	return u.Scheme+"://"+u.Host == c.Config.ServiceOrigin && u.RawQuery == "" && !u.ForceQuery && u.Path != "" && u.RawPath == "" && !strings.Contains(u.Path, "//") && !strings.Contains(u.Path, "..")
}
func (c *Client) Authorize(state, nonce, hint, message string) string {
	u, _ := url.Parse(c.Config.AuthURL)
	q := u.Query()
	q.Set("scope", "openid")
	q.Set("response_type", "id_token")
	q.Set("response_mode", "form_post")
	q.Set("prompt", "none")
	q.Set("client_id", c.Config.ClientID)
	q.Set("redirect_uri", c.Config.BaseURL+"/lti/launch")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("login_hint", hint)
	if message != "" {
		q.Set("lti_message_hint", message)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func PublicJWK(key *rsa.PublicKey, kid string) JWK {
	return JWK{"RSA", kid, "sig", "RS256", base64.RawURLEncoding.EncodeToString(key.N.Bytes()), base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}
func (c *Client) JWKS() any {
	return map[string]any{"keys": []JWK{PublicJWK(&c.key.PublicKey, c.Config.KeyID)}}
}
func readJSON(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrRemote
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
	if err != nil || len(raw) > 128*1024 {
		return ErrRemote
	}
	if out == nil {
		return nil
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrInvalid
	}
	return nil
}
func (c *Client) do(ctx context.Context, method, raw, token, content, accept string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, raw, bytes.NewReader(body))
	if err != nil {
		return ErrInvalid
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if content != "" {
		req.Header.Set("Content-Type", content)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrRemote
	}
	if strings.Contains(resp.Header.Get("Link"), "rel=\"next\"") || strings.Contains(resp.Header.Get("Link"), "rel=next") {
		resp.Body.Close()
		return ErrInvalid
	}
	return readJSON(resp, out)
}
func (c *Client) platformKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []JWK `json:"keys"`
	}
	if e := c.do(ctx, "GET", c.Config.JWKSURL, "", "", "application/json", nil, &set); e != nil {
		return nil, e
	}
	if len(set.Keys) > 16 {
		return nil, ErrInvalid
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(k.N)
		e, e2 := base64.RawURLEncoding.DecodeString(k.E)
		if e1 != nil || e2 != nil || len(e) > 4 || len(n) > 1024 || len(n) < 256 || k.Kid == "" {
			return nil, ErrInvalid
		}
		ex := new(big.Int).SetBytes(e).Int64()
		if ex < 3 || ex%2 == 0 {
			return nil, ErrInvalid
		}
		if keys[k.Kid] != nil {
			return nil, ErrInvalid
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(ex)}
	}
	return keys, nil
}

type LaunchClaims struct {
	jwt.RegisteredClaims
	Nonce           string   `json:"nonce"`
	AuthorizedParty string   `json:"azp"`
	Version         string   `json:"https://purl.imsglobal.org/spec/lti/claim/version"`
	MessageType     string   `json:"https://purl.imsglobal.org/spec/lti/claim/message_type"`
	DeploymentID    string   `json:"https://purl.imsglobal.org/spec/lti/claim/deployment_id"`
	Target          string   `json:"https://purl.imsglobal.org/spec/lti/claim/target_link_uri"`
	Roles           []string `json:"https://purl.imsglobal.org/spec/lti/claim/roles"`
	Context         struct {
		ID string `json:"id"`
	} `json:"https://purl.imsglobal.org/spec/lti/claim/context"`
	Resource struct {
		ID string `json:"id"`
	} `json:"https://purl.imsglobal.org/spec/lti/claim/resource_link"`
	Endpoint struct {
		Scope    []string `json:"scope"`
		LineItem string   `json:"lineitem"`
	} `json:"https://purl.imsglobal.org/spec/lti-ags/claim/endpoint"`
}
type Launch struct {
	Subject       string `json:"-"`
	Context       string `json:"context"`
	Resource      string `json:"resource"`
	LineItem      string `json:"lineitem"`
	Role          string `json:"role"`
	Registration  string `json:"registration"`
	LineItemScope string `json:"lineitem_scope"`
}

func (c *Client) Verify(ctx context.Context, raw, nonce string) (*Launch, error) {
	if len(raw) > 32768 || nonce == "" {
		return nil, ErrInvalid
	}
	keys, err := c.platformKeys(ctx)
	if err != nil {
		return nil, err
	}
	claims := &LaunchClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		key := keys[kid]
		if key == nil {
			return nil, ErrInvalid
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(c.Config.Issuer), jwt.WithAudience(c.Config.ClientID), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
	if err != nil || !token.Valid || claims.IssuedAt == nil || time.Since(claims.IssuedAt.Time) > 10*time.Minute || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > time.Hour || claims.Nonce != nonce || claims.Version != "1.3.0" || claims.MessageType != "LtiResourceLinkRequest" || claims.DeploymentID != c.Config.DeploymentID || claims.Target != c.Config.BaseURL+"/lti/launch" {
		return nil, ErrInvalid
	}
	if (claims.AuthorizedParty != "" && claims.AuthorizedParty != c.Config.ClientID) || (len(claims.Audience) > 1 && claims.AuthorizedParty != c.Config.ClientID) {
		return nil, ErrInvalid
	}
	for _, v := range []string{claims.Subject, claims.Context.ID, claims.Resource.ID} {
		if v == "" || len(v) > 256 {
			return nil, ErrInvalid
		}
	}
	role := ""
	for _, r := range claims.Roles {
		if r == Instructor || r == Learner {
			if role != "" && role != r {
				return nil, ErrInvalid
			}
			role = r
		}
	}
	if role == "" || !c.ServiceURL(claims.Endpoint.LineItem) {
		return nil, ErrInvalid
	}
	for _, required := range []string{"score", "result.readonly", "lineitem.readonly"} {
		found := false
		for _, scope := range claims.Endpoint.Scope {
			if scope == AGS+"scope/"+required || (required == "lineitem.readonly" && scope == AGS+"scope/lineitem") {
				found = true
			}
		}
		if !found {
			return nil, ErrInvalid
		}
	}
	lineScope := AGS + "scope/lineitem"
	for _, scope := range claims.Endpoint.Scope {
		if scope == AGS+"scope/lineitem.readonly" {
			lineScope = scope
		}
	}
	return &Launch{Subject: claims.Subject, Context: claims.Context.ID, Resource: claims.Resource.ID, LineItem: claims.Endpoint.LineItem, Role: role, Registration: c.Registration, LineItemScope: lineScope}, nil
}
func (c *Client) accessToken(ctx context.Context, launch Launch) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{Issuer: c.Config.ClientID, Subject: c.Config.ClientID, Audience: jwt.ClaimStrings{c.Config.TokenAudience}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)), ID: id.New()}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = c.Config.KeyID
	assertion, err := t.SignedString(c.key)
	if err != nil {
		return "", ErrInvalid
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, "client_assertion": {assertion}, "scope": {AGS + "scope/score " + AGS + "scope/result.readonly " + launch.LineItemScope}}
	var result struct {
		Token string `json:"access_token"`
		Type  string `json:"token_type"`
		Scope string `json:"scope"`
	}
	err = c.do(ctx, "POST", c.Config.TokenURL, "", "application/x-www-form-urlencoded", "application/json", []byte(form.Encode()), &result)
	if err != nil {
		return "", err
	}
	if result.Token == "" || len(result.Token) > 16384 || !strings.EqualFold(result.Type, "Bearer") {
		return "", ErrInvalid
	}
	if result.Scope != "" {
		scopes := strings.Fields(result.Scope)
		for _, required := range strings.Fields(form.Get("scope")) {
			ok := false
			for _, s := range scopes {
				if s == required {
					ok = true
				}
			}
			if !ok {
				return "", ErrInvalid
			}
		}
	}
	return result.Token, nil
}

type Result struct {
	UserID  string   `json:"userId"`
	Score   *float64 `json:"resultScore"`
	Maximum *float64 `json:"resultMaximum"`
}

func (r Result) Ratio() (float64, bool) {
	if r.Score == nil && r.Maximum == nil {
		return 0, false
	}
	if r.Score == nil || r.Maximum == nil || !finite(*r.Score) || !finite(*r.Maximum) || *r.Maximum <= 0 || *r.Score < 0 {
		return math.NaN(), true
	}
	return *r.Score / *r.Maximum, true
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func (c *Client) Read(ctx context.Context, l Launch, subject, token string) (Result, error) {
	if !c.ServiceURL(l.LineItem) {
		return Result{}, ErrInvalid
	}
	var line struct {
		ID       string  `json:"id"`
		Resource string  `json:"resourceLinkId"`
		Maximum  float64 `json:"scoreMaximum"`
	}
	if err := c.do(ctx, "GET", l.LineItem, token, "", "application/vnd.ims.lis.v2.lineitem+json", nil, &line); err != nil {
		return Result{}, err
	}
	if line.ID != l.LineItem || line.Resource != l.Resource || !finite(line.Maximum) || line.Maximum <= 0 {
		return Result{}, ErrInvalid
	}
	var results []Result
	endpoint := l.LineItem + "/results?" + url.Values{"user_id": {subject}, "limit": {"2"}}.Encode()
	if err := c.do(ctx, "GET", endpoint, token, "", "application/vnd.ims.lis.v2.resultcontainer+json", nil, &results); err != nil {
		return Result{}, err
	}
	if len(results) == 0 {
		return Result{}, nil
	}
	if len(results) != 1 || results[0].UserID != subject {
		return Result{}, ErrInvalid
	}
	ratio, _ := results[0].Ratio()
	if !finite(ratio) {
		return Result{}, ErrInvalid
	}
	return results[0], nil
}
func (c *Client) Send(ctx context.Context, l Launch, subject, token, timestamp string, score, maximum float64) error {
	if !c.ServiceURL(l.LineItem) || !finite(score) || !finite(maximum) || score < 0 || maximum <= 0 || score > maximum {
		return ErrInvalid
	}
	body, _ := json.Marshal(map[string]any{"userId": subject, "timestamp": timestamp, "scoreGiven": score, "scoreMaximum": maximum, "activityProgress": "Completed", "gradingProgress": "FullyGraded"})
	return c.do(ctx, "POST", l.LineItem+"/scores", token, "application/vnd.ims.lis.v1.score+json", "", body, nil)
}
func (c *Client) String() string {
	return fmt.Sprintf("LTI %s (simulation=%v)", c.Config.ClientID, c.Config.Simulation)
}
