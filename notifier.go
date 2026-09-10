package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type notificationError struct {
	message     string
	retryable   bool
	retryDelays []time.Duration
}

func (err notificationError) Error() string {
	return err.message
}

type FeishuNotifier struct {
	config      Config
	client      *http.Client
	logger      *log.Logger
	retryDelays []time.Duration
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
}

func NewFeishuNotifier(config Config, logger *log.Logger) *FeishuNotifier {
	return &FeishuNotifier{
		config:      config,
		client:      &http.Client{Timeout: config.HTTPTimeout},
		logger:      logger,
		retryDelays: []time.Duration{time.Second, 3 * time.Second, 10 * time.Second},
		now:         time.Now,
		sleep: func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (notifier *FeishuNotifier) buildPayload(text string) ([]byte, error) {
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	if notifier.config.WebhookSecret != "" {
		timestamp := strconv.FormatInt(notifier.now().Unix(), 10)
		key := []byte(timestamp + "\n" + notifier.config.WebhookSecret)
		signer := hmac.New(sha256.New, key)
		payload["timestamp"] = timestamp
		payload["sign"] = base64.StdEncoding.EncodeToString(signer.Sum(nil))
	}
	return json.Marshal(payload)
}

func (notifier *FeishuNotifier) Send(ctx context.Context, text string) bool {
	body, err := notifier.buildPayload(text)
	if err != nil {
		notifier.logger.Printf("ERROR 构造飞书消息失败: %v", err)
		return false
	}

	retryAttempts := make(map[string]int)
	for {
		sendErr := notifier.post(ctx, body)
		if sendErr == nil {
			return true
		}
		if !sendErr.retryable {
			notifier.logger.Printf("ERROR 发送飞书消息失败: %s", sendErr.message)
			return false
		}
		retryClass := "default"
		retryDelays := notifier.retryDelays
		if len(sendErr.retryDelays) > 0 {
			retryClass = sendErr.message
			retryDelays = sendErr.retryDelays
		}
		attempt := retryAttempts[retryClass]
		if attempt >= len(retryDelays) {
			notifier.logger.Printf("ERROR 发送飞书消息失败: %s", sendErr.message)
			return false
		}
		delay := retryDelays[attempt]
		retryAttempts[retryClass] = attempt + 1
		notifier.logger.Printf("WARN 发送飞书消息失败，%s 后重试: %s", delay, sendErr.message)
		if err := notifier.sleep(ctx, delay); err != nil {
			return false
		}
	}
}

func (notifier *FeishuNotifier) post(ctx context.Context, body []byte) *notificationError {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, notifier.config.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return &notificationError{message: "无法创建 HTTP 请求"}
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := notifier.client.Do(request)
	if err != nil {
		return &notificationError{message: fmt.Sprintf("网络请求失败: %T", err), retryable: true}
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 20*1024))
	if readErr != nil {
		return &notificationError{message: "读取响应失败", retryable: true}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &notificationError{
			message:   fmt.Sprintf("HTTP %d", response.StatusCode),
			retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
		}
	}

	var result map[string]any
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return &notificationError{message: "飞书返回了无效 JSON"}
	}
	code, found := responseCode(result)
	if !found {
		return &notificationError{message: "飞书响应缺少状态码"}
	}
	if code != 0 {
		if code == 11232 {
			return &notificationError{
				message:     "飞书业务错误码 11232（系统限流）",
				retryable:   true,
				retryDelays: []time.Duration{time.Minute, 3 * time.Minute, 5 * time.Minute},
			}
		}
		return &notificationError{message: fmt.Sprintf("飞书业务错误码 %d", code)}
	}
	return nil
}

func responseCode(result map[string]any) (int64, bool) {
	for _, key := range []string{"code", "StatusCode"} {
		value, ok := result[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int64(typed), true
		case string:
			parsed, err := strconv.ParseInt(typed, 10, 64)
			return parsed, err == nil
		}
	}
	return 0, false
}
