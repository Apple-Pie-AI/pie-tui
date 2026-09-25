// Package jira is a minimal Jira Cloud REST client (read + comment) for the MVP.
package jira

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a Jira Cloud instance. If Email is set we use basic auth
// (email:api-token); otherwise a bearer token.
type Client struct {
	BaseURL string
	Email   string
	Token   string
	http    *http.Client
}

func New(baseURL, email, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Email:   email,
		Token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Issue is the slice of a ticket the agent needs.
type Issue struct {
	Key         string
	Summary     string
	Description string
	Status      string // e.g. "To Do", "In Progress", "Done"
}

func (c *Client) auth(req *http.Request) {
	if c.Email != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(c.Email + ":" + c.Token))
		req.Header.Set("Authorization", "Basic "+cred)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
}

// FetchIssue retrieves summary + description for a ticket key.
func (c *Client) FetchIssue(key string) (*Issue, error) {
	u := fmt.Sprintf("%s/rest/api/2/issue/%s?fields=summary,description,status", c.BaseURL, url.PathEscape(key))
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jira fetch %s: HTTP %d: %s", key, resp.StatusCode, truncate(string(body), 300))
	}
	var raw struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string          `json:"summary"`
			Description json.RawMessage `json:"description"`
			Status      struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return &Issue{
		Key:         raw.Key,
		Summary:     raw.Fields.Summary,
		Description: flattenDesc(raw.Fields.Description),
		Status:      raw.Fields.Status.Name,
	}, nil
}

// Verify checks credentials with a lightweight authenticated call (/myself).
func (c *Client) Verify() error {
	req, _ := http.NewRequest(http.MethodGet, c.BaseURL+"/rest/api/2/myself", nil)
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return nil
}

// AddComment posts a plain-text comment on a ticket.
func (c *Client) AddComment(key, body string) error {
	u := fmt.Sprintf("%s/rest/api/2/issue/%s/comment", c.BaseURL, url.PathEscape(key))
	payload, _ := json.Marshal(map[string]string{"body": body})
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(string(payload)))
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jira comment %s: HTTP %d: %s", key, resp.StatusCode, truncate(string(b), 200))
	}
	return nil
}

// flattenDesc handles both a plain-string description (classic api/2) and an
// Atlassian Document Format object (Jira Cloud), best-effort extracting text.
func flattenDesc(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '"' {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
	}
	var v interface{}
	if json.Unmarshal(raw, &v) == nil {
		var sb strings.Builder
		collectText(v, &sb)
		return strings.TrimSpace(sb.String())
	}
	return s
}

func collectText(v interface{}, sb *strings.Builder) {
	switch t := v.(type) {
	case map[string]interface{}:
		if txt, ok := t["text"].(string); ok {
			sb.WriteString(txt)
		}
		if content, ok := t["content"]; ok {
			collectText(content, sb)
		}
		if t["type"] == "paragraph" {
			sb.WriteString("\n")
		}
	case []interface{}:
		for _, child := range t {
			collectText(child, sb)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
