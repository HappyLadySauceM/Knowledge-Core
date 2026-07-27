package nats

import "time"

type Config struct {
	URL            string
	Name           string
	ConnectTimeout time.Duration
	MaxReconnects  int
	ReconnectWait  time.Duration
}
