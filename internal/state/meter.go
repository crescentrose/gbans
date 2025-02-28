// Package state is used for exporting state or other stats to prometheus.
package state

import (
	"context"
	"github.com/leighmacdonald/gbans/internal/event"
	"github.com/leighmacdonald/gbans/internal/model"
	"github.com/leighmacdonald/gbans/pkg/logparse"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	damageCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gbans_game_damage",
			Help: "Total (real)damage dealt",
		},
		[]string{"server_name", "steam_id", "target_id", "weapon"})
	healingCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gbans_game_healing",
			Help: "Total (real)healing",
		},
		[]string{"server_name", "steam_id", "target_id", "healing"})
	killCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gbans_game_kills",
			Help: "Total kills",
		},
		[]string{"server_name", "steam_id", "target_id", "weapon"})
	shotFiredCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gbans_game_shot_fired",
			Help: "Total shots fired",
		},
		[]string{"server_name", "steam_id", "weapon"})
	shotHitCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gbans_game_shot_hit",
			Help: "Total shots hit",
		},
		[]string{"server_name", "steam_id", "weapon"})
)

func init() {
	for _, m := range []prometheus.Collector{
		damageCounter,
		healingCounter,
		killCounter,
		shotFiredCounter,
		shotHitCounter,
	} {
		_ = prometheus.Register(m)
	}
}

// TODO fix race condition reading values
func LogMeter(ctx context.Context) {
	c := make(chan model.ServerEvent)
	if err := event.RegisterConsumer(c, []logparse.MsgType{
		logparse.ShotHit,
		logparse.ShotFired,
		logparse.Damage,
		logparse.Killed,
		logparse.Healed,
	}); err != nil {
		log.Errorf("Failed to register event consumer")
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-c:
			switch e.EventType {
			case logparse.Damage:
				damageCounter.With(prometheus.Labels{
					"server_name": e.Server.ServerName,
					"steam_id":    e.Source.SteamID.String(),
					"target_id":   e.Target.SteamID.String(),
					"weapon":      e.Weapon.String()}).
					Add(float64(e.Damage))
			case logparse.Healed:
				healingCounter.With(prometheus.Labels{
					"server_name": e.Server.ServerName,
					"steam_id":    e.Source.SteamID.String(),
					"target_id":   e.Target.SteamID.String()}).
					Add(float64(e.Damage))
			case logparse.ShotFired:
				shotFiredCounter.With(prometheus.Labels{
					"server_name": e.Server.ServerName,
					"steam_id":    e.Source.SteamID.String(),
					"weapon":      e.Weapon.String()}).
					Inc()
			case logparse.ShotHit:
				shotHitCounter.With(prometheus.Labels{
					"server_name": e.Server.ServerName,
					"steam_id":    e.Source.SteamID.String(),
					"weapon":      e.Weapon.String()}).
					Inc()
			case logparse.Killed:
				killCounter.With(prometheus.Labels{
					"server_name": e.Server.ServerName,
					"steam_id":    e.Source.SteamID.String(),
					"target_id":   e.Target.SteamID.String(),
					"weapon":      e.Weapon.String()}).
					Inc()
			}

		}
	}
}
