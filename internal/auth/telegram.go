package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TelegramAuth struct {
	botToken string
	maxAge   time.Duration
}

type TelegramIdentity struct {
	UserID   string
	Username string
}

func NewTelegramAuth(botToken string, maxAge time.Duration) *TelegramAuth {
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	return &TelegramAuth{botToken: botToken, maxAge: maxAge}
}

func (a *TelegramAuth) VerifyWebAppInitData(initData string) (TelegramIdentity, error) {
	if strings.TrimSpace(a.botToken) == "" {
		return TelegramIdentity{}, errors.New("telegram bot token is missing")
	}
	initData = strings.TrimSpace(initData)
	if initData == "" {
		return TelegramIdentity{}, errors.New("telegram init data is empty")
	}

	vals, err := url.ParseQuery(initData)
	if err != nil {
		return TelegramIdentity{}, fmt.Errorf("parse telegram init data: %w", err)
	}
	hashHex := vals.Get("hash")
	if hashHex == "" {
		return TelegramIdentity{}, errors.New("telegram init data missing hash")
	}

	parts := make([]string, 0, len(vals))
	for key, list := range vals {
		if key == "hash" {
			continue
		}
		if len(list) == 0 {
			continue
		}
		parts = append(parts, key+"="+list[0])
	}
	sort.Strings(parts)
	dataCheckString := strings.Join(parts, "\n")

	secretKey := hmacSHA256([]byte("WebAppData"), []byte(a.botToken))
	expected := hmacSHA256(secretKey, []byte(dataCheckString))
	actual, decodeErr := hex.DecodeString(hashHex)
	if decodeErr != nil {
		return TelegramIdentity{}, errors.New("telegram init data has invalid hash encoding")
	}
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		return TelegramIdentity{}, errors.New("telegram init data signature mismatch")
	}

	authDateRaw := vals.Get("auth_date")
	authDateUnix, parseErr := strconv.ParseInt(authDateRaw, 10, 64)
	if parseErr != nil {
		return TelegramIdentity{}, errors.New("telegram init data has invalid auth_date")
	}
	authAt := time.Unix(authDateUnix, 0).UTC()
	if time.Since(authAt) > a.maxAge {
		return TelegramIdentity{}, errors.New("telegram init data has expired")
	}

	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if unmarshalErr := json.Unmarshal([]byte(vals.Get("user")), &user); unmarshalErr != nil {
		return TelegramIdentity{}, errors.New("telegram init data user payload invalid")
	}
	if user.ID == 0 {
		return TelegramIdentity{}, errors.New("telegram init data missing user id")
	}

	return TelegramIdentity{
		UserID:   strconv.FormatInt(user.ID, 10),
		Username: user.Username,
	}, nil
}

func hmacSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}
