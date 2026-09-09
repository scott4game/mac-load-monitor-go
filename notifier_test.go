package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func notifierTestConfig(webhookURL string) Config {
	return Config{WebhookURL: webhookURL, HTTPTimeout: time.Second}
}

func TestBuildPayloadWithSignature(t *testing.T) {
	config := notifierTestConfig("https://open.feishu.cn/open-apis/bot/v2/hook/test")
	config.WebhookSecret = "secret"
	notifier := NewFeishuNotifier(config, log.New(io.Discard, "", 0))
	notifier.now = func() time.Time { return time.Unix(1700000000, 0) }
	body, err := notifier.buildPayload("hello")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	key := []byte("1700000000\nsecret")
	signer := hmac.New(sha256.New, key)
	expected := base64.StdEncoding.EncodeToString(signer.Sum(nil))
	if payload["timestamp"] != "1700000000" || payload["sign"] != expected {
		t.Fatalf("unexpected signed payload: %+v", payload)
	}
}

func TestNotifierAcceptsCurrentAndLegacySuccess(t *testing.T) {
	responses := []string{`{"code":0,"msg":"success"}`, `{"StatusCode":0,"StatusMessage":"success"}`}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(responses[0]))
		responses = responses[1:]
	}))
	defer server.Close()
	notifier := NewFeishuNotifier(notifierTestConfig(server.URL), log.New(io.Discard, "", 0))
	if !notifier.Send(context.Background(), "one") || !notifier.Send(context.Background(), "two") {
		t.Fatal("expected successful notifications")
	}
}

func TestNotifierRetriesServerFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(writer, "temporary", http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()
	notifier := NewFeishuNotifier(notifierTestConfig(server.URL), log.New(io.Discard, "", 0))
	notifier.retryDelays = []time.Duration{time.Second, 3 * time.Second}
	notifier.sleep = func(context.Context, time.Duration) error { return nil }
	if !notifier.Send(context.Background(), "hello") || attempts != 3 {
		t.Fatalf("expected success after three attempts, got %d", attempts)
	}
}

func TestNotifierDoesNotRetryBusinessErrorOrLeakWebhook(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		_, _ = writer.Write([]byte(`{"code":19021,"msg":"bad sign"}`))
	}))
	defer server.Close()
	var logs strings.Builder
	config := notifierTestConfig(server.URL + "/very-secret-token")
	notifier := NewFeishuNotifier(config, log.New(&logs, "", 0))
	if notifier.Send(context.Background(), "hello") || attempts != 1 {
		t.Fatalf("expected one failed attempt, got %d", attempts)
	}
	if strings.Contains(logs.String(), config.WebhookURL) || strings.Contains(logs.String(), "very-secret-token") {
		t.Fatalf("webhook leaked in logs: %s", logs.String())
	}
}

func TestNotifierRejectsInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("not-json"))
	}))
	defer server.Close()
	notifier := NewFeishuNotifier(notifierTestConfig(server.URL), log.New(io.Discard, "", 0))
	if notifier.Send(context.Background(), "hello") {
		t.Fatal("expected invalid JSON failure")
	}
}

func TestNotifierRetriesTimeoutWithoutLeakingWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = writer.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()
	var logs strings.Builder
	config := notifierTestConfig(server.URL + "/private-hook")
	config.HTTPTimeout = time.Millisecond
	notifier := NewFeishuNotifier(config, log.New(&logs, "", 0))
	notifier.retryDelays = []time.Duration{time.Millisecond}
	notifier.sleep = func(context.Context, time.Duration) error { return nil }
	if notifier.Send(context.Background(), "hello") {
		t.Fatal("expected timeout failure")
	}
	if strings.Contains(logs.String(), config.WebhookURL) || strings.Contains(logs.String(), "private-hook") {
		t.Fatalf("webhook leaked in timeout logs: %s", logs.String())
	}
}
