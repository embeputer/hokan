package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func New() *Client {
	savedServer, savedToken := loadSaved()
	base := os.Getenv("HOKAN_SERVER")
	if base == "" {
		base = savedServer
	}
	if base == "" {
		base = "http://localhost:8080"
	}
	token := os.Getenv("HOKAN_TOKEN")
	if token == "" {
		token = savedToken
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "hokan", "config.json")
}

func loadSaved() (server, token string) {
	b, err := os.ReadFile(configPath())
	if err != nil {
		return "", ""
	}
	var cfg struct {
		Server string `json:"server"`
		Token  string `json:"token"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return "", ""
	}
	return cfg.Server, cfg.Token
}

func SaveCredentials(server, token string) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]string{"server": server, "token": token}, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

func (c *Client) Do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("server %s: %s", res.Status, raw)
	}
	if env.Error != nil && env.Error.Message != "" {
		return fmt.Errorf("%s", env.Error.Message)
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("http %d: %s", res.StatusCode, raw)
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func (c *Client) Get(path string, q url.Values, out any) error {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.Do(http.MethodGet, path, nil, out)
}
