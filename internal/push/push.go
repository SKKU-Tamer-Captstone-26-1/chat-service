package push

import "context"

type Message struct {
	Token string
	Title string
	Body  string
	Data  map[string]string
}

type Sender interface {
	Send(ctx context.Context, msg Message) error
}
