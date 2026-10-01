package service

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/btrvodka/redigate/internal/codec"
)

// PubSub introspection: every node knows only its own subscribers, so the
// commands run on all data nodes and the results are merged.

type PubSubChannelsResult struct {
	Channels []any `json:"channels"`
	// Errors lists nodes that failed, the result is partial then.
	Errors map[string]string `json:"errors,omitempty"`
}

func (s *Service) PubSubChannels(ctx context.Context, pattern string, sharded bool, encoding codec.Encoding) (*PubSubChannelsResult, error) {
	args := []any{"pubsub", "channels"}
	if sharded {
		args[1] = "shardchannels"
	}

	if pattern != "" {
		args = append(args, pattern)
	}

	res, err := s.perNodeCommand(ctx, NodeSelector{}, TargetAll, okRaw, args...)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	result := &PubSubChannelsResult{Channels: []any{}, Errors: nodeErrors(res)}

	for _, node := range res.Nodes {
		names, _ := node.Value.([]any)
		for _, name := range names {
			seen[fmt.Sprint(name)] = true
		}
	}

	enc := codec.NewEncoder(encoding, s.limits.MaxResponseItems)
	for _, name := range slices.Sorted(maps.Keys(seen)) {
		result.Channels = append(result.Channels, enc.String(name))
	}

	return result, nil
}

type PubSubCountResult struct {
	// Counts are subscribers per channel for NUMSUB, Count is the number of patterns for NUMPAT.
	Counts any               `json:"counts,omitempty"`
	Count  *int64            `json:"count,omitempty"`
	Errors map[string]string `json:"errors,omitempty"`
}

func (s *Service) PubSubNumSub(ctx context.Context, channels []string, sharded bool, encoding codec.Encoding) (*PubSubCountResult, error) {
	if len(channels) == 0 {
		return nil, fmt.Errorf("%w: channel is required", ErrInvalidRequest)
	}

	name := "numsub"
	if sharded {
		name = "shardnumsub"
	}

	res, err := s.perNodeCommand(ctx, NodeSelector{}, TargetAll, okRaw, commandArgs([]any{"pubsub", name}, channels)...)
	if err != nil {
		return nil, err
	}

	totals := make(map[any]any, len(channels))
	for _, channel := range channels {
		totals[channel] = int64(0)
	}

	for _, node := range res.Nodes {
		counts, _ := ShapeMap(node.Value).(map[any]any)
		for channel, count := range counts {
			key := fmt.Sprint(channel)
			totals[key] = totals[key].(int64) + toInt64(count) //nolint:forcetypeassert // initialized above
		}
	}

	enc := codec.NewEncoder(encoding, s.limits.MaxResponseItems)

	return &PubSubCountResult{Counts: enc.Encode(totals), Errors: nodeErrors(res)}, nil
}

func (s *Service) PubSubNumPat(ctx context.Context) (*PubSubCountResult, error) {
	res, err := s.perNodeCommand(ctx, NodeSelector{}, TargetAll, okRaw, "pubsub", "numpat")
	if err != nil {
		return nil, err
	}

	var total int64
	for _, node := range res.Nodes {
		total += toInt64(node.Value)
	}

	return &PubSubCountResult{Count: &total, Errors: nodeErrors(res)}, nil
}

func okRaw(reply any) (any, error) {
	return reply, nil
}

func nodeErrors(res *FanOutResult) map[string]string {
	var errs map[string]string

	for _, node := range res.Nodes {
		if node.Error != "" {
			if errs == nil {
				errs = make(map[string]string)
			}

			errs[node.Addr] = node.Error
		}
	}

	return errs
}
