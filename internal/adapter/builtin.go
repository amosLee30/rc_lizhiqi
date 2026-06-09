package adapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// nowUnix is overridable in tests for deterministic signing.
var nowUnix = func() int64 { return time.Now().Unix() }

// BearerAdapter is a thin adapter for suppliers using a static bearer token.
// It demonstrates "simple supplier = thin layer over BaseAdapter".
type BearerAdapter struct {
	BaseAdapter
}

// NewBearerAdapter builds a static-token adapter.
func NewBearerAdapter(typ, endpoint, method string, required []string) *BearerAdapter {
	return &BearerAdapter{BaseAdapter{TypeName: typ, Endpoint: endpoint, Method: method, Required: required}}
}

func (a *BearerAdapter) Type() string { return a.TypeName }

func (a *BearerAdapter) BuildRequest(params map[string]any, secret string) (*Request, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	h := map[string]string{"Content-Type": "application/json"}
	if secret != "" {
		h["Authorization"] = "Bearer " + secret
	}
	return &Request{Method: a.Method, URL: a.Endpoint, Headers: h, Body: body}, nil
}

// HMACAdapter is a bespoke adapter: it signs the body with secret + timestamp
// at delivery time (signature cannot be frozen, hence late binding). Different
// suppliers' signing schemes get their own class like this.
type HMACAdapter struct {
	BaseAdapter
}

// NewHMACAdapter builds an HMAC-signing adapter.
func NewHMACAdapter(typ, endpoint, method string, required []string) *HMACAdapter {
	return &HMACAdapter{BaseAdapter{TypeName: typ, Endpoint: endpoint, Method: method, Required: required}}
}

func (a *HMACAdapter) Type() string { return a.TypeName }

func (a *HMACAdapter) BuildRequest(params map[string]any, secret string) (*Request, error) {
	if secret == "" {
		return nil, fmt.Errorf("%s: missing signing secret", a.TypeName)
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(nowUnix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	h := map[string]string{
		"Content-Type": "application/json",
		"X-Timestamp":  ts,
		"X-Signature":  sig,
	}
	return &Request{Method: a.Method, URL: a.Endpoint, Headers: h, Body: body}, nil
}
