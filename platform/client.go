package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client calls the platform (Next.js) balance APIs using the user's JWT.
type Client struct {
	baseURL     string
	gameName    string
	gameProvider string
	http        *http.Client
}

func NewClient(baseURL, gameName, gameProvider string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	if gameName == "" {
		gameName = "Hi/Lo"
	}
	if gameProvider == "" {
		gameProvider = "Crypto LATAM"
	}
	return &Client{
		baseURL:     baseURL,
		gameName:    gameName,
		gameProvider: gameProvider,
		http:        &http.Client{},
	}
}

func (c *Client) authHeader(token string) string {
	return "Bearer " + token
}

// GetBalance returns the user's balances. Token = JWT from platform.
func (c *Client) GetBalance(token string) (map[string]interface{}, int, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/balance", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", c.authHeader(token))
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Balances map[string]interface{} `json:"balances"`
		Error    string                 `json:"error"`
	}
	_ = json.Unmarshal(body, &data)
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("platform: %s", data.Error)
	}
	return data.Balances, resp.StatusCode, nil
}

// Bet places a bet (debit). Returns betId and updated balances.
// gameName and gameProvider override client defaults when non-empty (e.g. "Scratch" for scratch rounds).
func (c *Client) Bet(token, currency string, amount float64, gameName, gameProvider string) (betID string, status int, err error) {
	if gameName == "" {
		gameName = c.gameName
	}
	if gameProvider == "" {
		gameProvider = c.gameProvider
	}
	payload := map[string]interface{}{
		"currency":      currency,
		"amount":        amount,
		"gameName":      gameName,
		"gameProvider":  gameProvider,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/balance/bet", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader(token))
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var data struct {
		BetID    string `json:"betId"`
		Balances map[string]interface{} `json:"balances"`
		Error    string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &data)
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("platform: %s", data.Error)
	}
	return data.BetID, resp.StatusCode, nil
}

// Win credits the user (win payout).
// gameName and gameProvider override client defaults when non-empty.
func (c *Client) Win(token, currency string, amount float64, gameName, gameProvider string) (status int, err error) {
	if gameName == "" {
		gameName = c.gameName
	}
	if gameProvider == "" {
		gameProvider = c.gameProvider
	}
	payload := map[string]interface{}{
		"currency":      currency,
		"amount":        amount,
		"gameName":      gameName,
		"gameProvider":  gameProvider,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/balance/win", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader(token))
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var data struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &data)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("platform: %s", data.Error)
	}
	return resp.StatusCode, nil
}

// Rollback refunds a bet.
func (c *Client) Rollback(token, betID string) (status int, err error) {
	payload := map[string]interface{}{"betId": betID}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/balance/rollback", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader(token))
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var data struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &data)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("platform: %s", data.Error)
	}
	return resp.StatusCode, nil
}
