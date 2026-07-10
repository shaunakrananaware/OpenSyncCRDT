package cluster

import (
	"context"
	"fmt"

	redis "github.com/redis/go-redis/v9"
)

const (
	// docChannelPrefix is the pub/sub channel namespace for per-document
	// operation broadcast: opensynccrdt:doc:<doc-id>.
	docChannelPrefix = "opensynccrdt:doc:"
	// nodeKeyPrefix is the key namespace for node-registry heartbeat keys:
	// opensynccrdt:nodes:<node-id>.
	nodeKeyPrefix = "opensynccrdt:nodes:"
)

// docChannel returns the pub/sub channel name for a document.
func docChannel(docID string) string { return docChannelPrefix + docID }

// nodeKey returns the registry key for a node.
func nodeKey(nodeID string) string { return nodeKeyPrefix + nodeID }

// redisBus wraps the Redis client and a single long-lived pub/sub subscription.
// Documents are subscribed and unsubscribed dynamically as local connections
// come and go, so a node only receives traffic for documents it actually serves.
type redisBus struct {
	client *redis.Client
	pubsub *redis.PubSub
}

// dialRedis connects to Redis, verifies the connection, and opens an empty
// pub/sub subscription that channels are added to on demand.
func dialRedis(ctx context.Context, url string) (*redisBus, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	// Subscribe with no channels: the SUBSCRIBE command is issued lazily as
	// documents become active. Channel() drives the receive goroutine.
	pubsub := client.Subscribe(ctx)
	return &redisBus{client: client, pubsub: pubsub}, nil
}

// publish sends a cross-node operation to a document channel.
func (b *redisBus) publish(ctx context.Context, channel string, data []byte) error {
	return b.client.Publish(ctx, channel, data).Err()
}

// subscribe starts receiving messages on a document channel.
func (b *redisBus) subscribe(ctx context.Context, channel string) error {
	return b.pubsub.Subscribe(ctx, channel)
}

// unsubscribe stops receiving messages on a document channel.
func (b *redisBus) unsubscribe(ctx context.Context, channel string) error {
	return b.pubsub.Unsubscribe(ctx, channel)
}

// messages returns the stream of incoming pub/sub messages across every
// currently-subscribed document channel.
func (b *redisBus) messages() <-chan *redis.Message {
	return b.pubsub.Channel()
}

// setNode writes (or refreshes) this node's registry key with the given TTL.
func (b *redisBus) setNode(ctx context.Context, key string, value []byte, ttl int) error {
	return b.client.Set(ctx, key, value, ttlDuration(ttl)).Err()
}

// listNodeValues returns the raw registry values for every currently-alive node.
// Keys that expire between SCAN and GET are skipped.
func (b *redisBus) listNodeValues(ctx context.Context) ([][]byte, error) {
	var out [][]byte
	iter := b.client.Scan(ctx, 0, nodeKeyPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		v, err := b.client.Get(ctx, iter.Val()).Bytes()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// deleteNode removes this node's registry key.
func (b *redisBus) deleteNode(ctx context.Context, key string) error {
	return b.client.Del(ctx, key).Err()
}

// close tears down the pub/sub subscription and the client.
func (b *redisBus) close() error {
	_ = b.pubsub.Close()
	return b.client.Close()
}
