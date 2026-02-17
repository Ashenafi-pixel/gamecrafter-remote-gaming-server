package operator

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

type Client struct {
	endpoint string
	secret   string
	http     *http.Client
}

type Response struct {
	Code        int             `json:"code"`
	Status      string          `json:"status"`
	Message     string          `json:"message"`
	Raw         json.RawMessage `json:"-"`
	Body        json.RawMessage `json:"-"`
	StatusCode  int             `json:"-"`
	ContentType string          `json:"-"`
}

func NewClient(endpoint, secret string) *Client {
	return &Client{
		endpoint: endpoint,
		secret:   secret,
		http:     &http.Client{},
	}
}

func (c *Client) call(params map[string]string) (*Response, error) {
	values := url.Values{}
	for k, v := range params {
		if v != "" {
			values.Set(k, v)
		}
	}
	if c.secret != "" {
		sig := c.sign(values)
		values.Set("signature", sig)
	}
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, err
	}
	u.RawQuery = values.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	var parsed struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)
	return &Response{
		Code:        parsed.Code,
		Status:      parsed.Status,
		Message:     parsed.Message,
		Raw:         body,
		Body:        body,
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

func (c *Client) sign(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		if k == "action" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := make([]byte, 0, 256)
	for _, k := range keys {
		buf = append(buf, v.Get(k)...)
	}
	m := hmac.New(sha256.New, []byte(c.secret))
	m.Write(buf)
	return hex.EncodeToString(m.Sum(nil))
}

func (c *Client) Account(playerID, sessionID, deviceType, apiVersion string) (*Response, error) {
	return c.call(map[string]string{
		"action":      "account",
		"player_id":   playerID,
		"session_id":  sessionID,
		"device_type": deviceType,
		"api_version": apiVersion,
	})
}

func (c *Client) Balance(playerID, sessionID, gameCode, deviceType, apiVersion string) (*Response, error) {
	return c.call(map[string]string{
		"action":      "balance",
		"player_id":   playerID,
		"session_id":  sessionID,
		"game_code":   gameCode,
		"device_type": deviceType,
		"api_version": apiVersion,
	})
}

func (c *Client) Debit(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion string, betAmount float64, bonusID string) (*Response, error) {
	return c.call(map[string]string{
		"action":      "debit",
		"player_id":   playerID,
		"session_id":  sessionID,
		"round_id":    roundID,
		"tx_id":       txID,
		"bet_amount":  formatAmount(betAmount),
		"game_code":   gameCode,
		"device_type": deviceType,
		"api_version": apiVersion,
		"bonus_id":    bonusID,
	})
}

func (c *Client) Credit(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion, roundStatus, bonusID string, winAmount float64) (*Response, error) {
	return c.call(map[string]string{
		"action":       "credit",
		"player_id":    playerID,
		"session_id":   sessionID,
		"round_id":     roundID,
		"tx_id":        txID,
		"win_amount":   formatAmount(winAmount),
		"round_status": roundStatus,
		"game_code":    gameCode,
		"device_type":  deviceType,
		"api_version":  apiVersion,
		"bonus_id":     bonusID,
	})
}

func (c *Client) DebitAndCredit(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion, roundStatus string, betAmount, winAmount float64) (*Response, error) {
	return c.call(map[string]string{
		"action":       "debit_and_credit",
		"player_id":    playerID,
		"session_id":   sessionID,
		"round_id":     roundID,
		"tx_id":        txID,
		"bet_amount":   formatAmount(betAmount),
		"win_amount":   formatAmount(winAmount),
		"round_status": roundStatus,
		"game_code":    gameCode,
		"device_type":  deviceType,
		"api_version":  apiVersion,
	})
}

func (c *Client) Refund(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion string, refundAmount float64) (*Response, error) {
	return c.call(map[string]string{
		"action":        "refund",
		"player_id":     playerID,
		"session_id":    sessionID,
		"round_id":      roundID,
		"tx_id":         txID,
		"refund_amount": formatAmount(refundAmount),
		"game_code":     gameCode,
		"device_type":   deviceType,
		"api_version":   apiVersion,
	})
}

func (c *Client) Jackpot(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion, roundStatus string, amount float64) (*Response, error) {
	return c.call(map[string]string{
		"action":       "jackpot",
		"player_id":    playerID,
		"session_id":   sessionID,
		"round_id":     roundID,
		"tx_id":        txID,
		"amount":       formatAmount(amount),
		"round_status": roundStatus,
		"game_code":    gameCode,
		"device_type":  deviceType,
		"api_version":  apiVersion,
	})
}

func (c *Client) ReverseWin(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion, winTxID string, amount float64) (*Response, error) {
	return c.call(map[string]string{
		"action":      "reverse_win",
		"player_id":   playerID,
		"session_id":  sessionID,
		"round_id":    roundID,
		"tx_id":       txID,
		"amount":      formatAmount(amount),
		"game_code":   gameCode,
		"device_type": deviceType,
		"api_version": apiVersion,
		"win_tx_id":   winTxID,
	})
}

func (c *Client) ReverseRefund(playerID, sessionID, roundID, txID, gameCode, deviceType, apiVersion string, refundAmount float64) (*Response, error) {
	return c.call(map[string]string{
		"action":        "reverse_refund",
		"player_id":     playerID,
		"session_id":    sessionID,
		"round_id":      roundID,
		"tx_id":         txID,
		"refund_amount": formatAmount(refundAmount),
		"game_code":     gameCode,
		"device_type":   deviceType,
		"api_version":   apiVersion,
	})
}

func formatAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
