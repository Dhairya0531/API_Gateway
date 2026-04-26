package config

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Dhairya0531/API_Gateway/internal/balancer"
	"github.com/Dhairya0531/API_Gateway/internal/circuitbreaker"
	"github.com/Dhairya0531/API_Gateway/internal/store"
)

// DynamicConfigPayload is the expected JSON payload published to Redis.
type DynamicConfigPayload struct {
	Routes   []RouteConfig             `json:"routes"`
	Services map[string]ServiceConfig  `json:"services"`
}

// RouterUpdater is an interface satisfied by router.Router to decouple packages.
type RouterUpdater interface {
	UpdateConfig(routes []RouteConfig, pools map[string]*balancer.Pool, cbManager *circuitbreaker.Manager) error
}

// WatchConfig subscribes to the "gateway:config:updates" Redis channel and hot-reloads the router when updates arrive.
func WatchConfig(ctx context.Context, redisClient *store.RedisClient, router RouterUpdater, cbManager *circuitbreaker.Manager, log *slog.Logger) {
	pubsub := redisClient.Client().Subscribe(ctx, "gateway:config:updates")
	defer pubsub.Close()

	ch := pubsub.Channel()
	log.Info("watching redis for dynamic configuration updates", slog.String("channel", "gateway:config:updates"))

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping config watcher")
			return
		case msg := <-ch:
			log.Info("received dynamic config update")
			
			var payload DynamicConfigPayload
			if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
				log.Error("failed to parse dynamic config payload", slog.String("error", err.Error()))
				continue
			}

			// Rebuild upstream pools from the new config
			newPools := make(map[string]*balancer.Pool)
			for name, svc := range payload.Services {
				pool := balancer.NewPool(svc.Upstreams)
				pool.SetStrategy(balancer.StrategyFromName(svc.BalanceStrategy))
				newPools[name] = pool
			}

			// Swap the router configuration atomically
			if err := router.UpdateConfig(payload.Routes, newPools, cbManager); err != nil {
				log.Error("failed to apply dynamic config update", slog.String("error", err.Error()))
			} else {
				log.Info("successfully applied dynamic config update", 
					slog.Int("routes", len(payload.Routes)), 
					slog.Int("pools", len(newPools)),
				)
			}
		}
	}
}
