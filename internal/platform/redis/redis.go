package redis

import (
	"context"
	"fmt"
	r "github.com/redis/go-redis/v9"
	"time"
)

type Client struct{ *r.Client }

func Open(raw string) (*Client, error) {
	o, e := r.ParseURL(raw)
	if e != nil {
		return nil, fmt.Errorf("invalid redis configuration")
	}
	o.DialTimeout = 3 * time.Second
	o.ReadTimeout = 3 * time.Second
	o.WriteTimeout = 3 * time.Second
	o.MaxRetries = -1
	o.ContextTimeoutEnabled = true
	return &Client{r.NewClient(o)}, nil
}
func (c *Client) Health(ctx context.Context) error { return c.Ping(ctx).Err() }
