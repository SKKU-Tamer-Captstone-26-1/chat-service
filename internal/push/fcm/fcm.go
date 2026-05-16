package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ontheblock/chat-service/internal/push"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const firebaseMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"

type Sender struct {
	projectID string
	client    *http.Client
}

func NewSender(ctx context.Context, projectID string) (*Sender, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("fcm project id is required")
	}
	tokenSource, err := google.DefaultTokenSource(ctx, firebaseMessagingScope)
	if err != nil {
		return nil, err
	}
	return &Sender{
		projectID: projectID,
		client:    oauth2.NewClient(ctx, tokenSource),
	}, nil
}

func (s *Sender) Send(ctx context.Context, msg push.Message) error {
	token := strings.TrimSpace(msg.Token)
	if token == "" {
		return fmt.Errorf("fcm token is required")
	}
	body, err := json.Marshal(fcmRequest{
		Message: fcmMessage{
			Token: token,
			Notification: fcmNotification{
				Title: msg.Title,
				Body:  msg.Body,
			},
			Data: msg.Data,
		},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("fcm send failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
}

func (s *Sender) url() string {
	return fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", s.projectID)
}

type fcmRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
