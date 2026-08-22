package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/model"
)

type masterClient struct {
	base string
	http *http.Client
}

func newMasterClient(base string) *masterClient {
	return &masterClient{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *masterClient) enroll(ctx context.Context, req model.EnrollRequest) (model.EnrollResponse, error) {
	var out model.EnrollResponse
	if c.base == "" {
		return out, fmt.Errorf("master_url is empty")
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/agents/enroll", "", req, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *masterClient) heartbeat(ctx context.Context, agentID, token string, snap model.AgentSnapshot) (model.HeartbeatResponse, error) {
	var out model.HeartbeatResponse
	reqBody, _ := json.Marshal(snap)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/agents/heartbeat", bytes.NewReader(reqBody))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Agent-ID", agentID)
	resp, err := c.http.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return out, fmt.Errorf("master heartbeat: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *masterClient) do(ctx context.Context, method, path, bearer string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("master: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
